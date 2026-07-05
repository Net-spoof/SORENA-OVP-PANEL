package main

import (
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	telegramBackupConfigPath = "/opt/irangate/webpanel/telegram_backup_schedule.json"
	telegramBackupScheduleID = "schedule_telegram_auto"
)

type TelegramBackupScheduleConfig struct {
	Enabled            bool       `json:"enabled"`
	IntervalValue      int        `json:"interval_value"`
	IntervalUnit       string     `json:"interval_unit"`
	RetentionDays      int        `json:"retention_days"`
	LastRun            *time.Time `json:"last_run,omitempty"`
	LastSuccess        *time.Time `json:"last_success,omitempty"`
	LastError          string     `json:"last_error,omitempty"`
	NextRun            *time.Time `json:"next_run,omitempty"`
	StoragePath        string     `json:"storage_path"`
	CredentialsPresent bool       `json:"credentials_present"`
}

type telegramBackupCredentials struct {
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

var (
	telegramBackupConfigMu sync.Mutex

	telegramBackupWake = make(
		chan struct{},
		1,
	)

	telegramBackupRunSemaphore = make(
		chan struct{},
		1,
	)
)

func defaultTelegramBackupConfig() TelegramBackupScheduleConfig {
	return TelegramBackupScheduleConfig{
		Enabled:       false,
		IntervalValue: 6,
		IntervalUnit:  "hours",
		RetentionDays: 30,
		StoragePath:   scheduledBackupDir(),
	}
}

func telegramBackupDuration(
	cfg TelegramBackupScheduleConfig,
) (time.Duration, error) {

	if cfg.IntervalValue <= 0 {
		return 0, fmt.Errorf(
			"interval value must be greater than zero",
		)
	}

	var duration time.Duration

	switch cfg.IntervalUnit {
	case "minutes":
		duration = time.Duration(
			cfg.IntervalValue,
		) * time.Minute

	case "hours":
		duration = time.Duration(
			cfg.IntervalValue,
		) * time.Hour

	default:
		return 0, fmt.Errorf(
			"interval unit must be minutes or hours",
		)
	}

	if duration < time.Minute {
		return 0, fmt.Errorf(
			"minimum interval is 1 minute",
		)
	}

	if duration > 30*24*time.Hour {
		return 0, fmt.Errorf(
			"maximum interval is 30 days",
		)
	}

	return duration, nil
}

func loadTelegramBackupConfigUnlocked() (
	TelegramBackupScheduleConfig,
	error,
) {
	cfg := defaultTelegramBackupConfig()

	data, err := os.ReadFile(
		telegramBackupConfigPath,
	)

	if os.IsNotExist(err) {
		return cfg, nil
	}

	if err != nil {
		return cfg, err
	}

	if err := json.Unmarshal(
		data,
		&cfg,
	); err != nil {
		return cfg, err
	}

	cfg.StoragePath = scheduledBackupDir()

	if cfg.IntervalValue <= 0 {
		cfg.IntervalValue = 6
	}

	if cfg.IntervalUnit != "minutes" &&
		cfg.IntervalUnit != "hours" {

		cfg.IntervalUnit = "hours"
	}

	if cfg.RetentionDays <= 0 {
		cfg.RetentionDays = 30
	}

	return cfg, nil
}

func loadTelegramBackupConfig() (
	TelegramBackupScheduleConfig,
	error,
) {
	telegramBackupConfigMu.Lock()
	defer telegramBackupConfigMu.Unlock()

	return loadTelegramBackupConfigUnlocked()
}

func saveTelegramBackupConfig(
	cfg TelegramBackupScheduleConfig,
) error {
	telegramBackupConfigMu.Lock()
	defer telegramBackupConfigMu.Unlock()

	cfg.StoragePath = scheduledBackupDir()
	cfg.CredentialsPresent = false

	if err := os.MkdirAll(
		filepath.Dir(
			telegramBackupConfigPath,
		),
		0700,
	); err != nil {

		return err
	}

	data, err := json.MarshalIndent(
		cfg,
		"",
		"  ",
	)

	if err != nil {
		return err
	}

	tempPath := telegramBackupConfigPath +
		".tmp"

	if err := os.WriteFile(
		tempPath,
		data,
		0600,
	); err != nil {

		return err
	}

	if err := os.Rename(
		tempPath,
		telegramBackupConfigPath,
	); err != nil {

		_ = os.Remove(tempPath)

		return err
	}

	return nil
}

func loadTelegramBackupCredentials() (
	telegramBackupCredentials,
	error,
) {
	var credentials telegramBackupCredentials

	data, err := os.ReadFile(
		TelegramDBPath,
	)

	if err != nil {
		return credentials, fmt.Errorf(
			"telegram settings are not configured",
		)
	}

	if err := json.Unmarshal(
		data,
		&credentials,
	); err != nil {

		return credentials, fmt.Errorf(
			"telegram settings are invalid",
		)
	}

	credentials.BotToken = strings.TrimSpace(
		credentials.BotToken,
	)

	credentials.ChatID = strings.TrimSpace(
		credentials.ChatID,
	)

	if credentials.BotToken == "" ||
		credentials.ChatID == "" {

		return credentials, fmt.Errorf(
			"Bot Token and Chat ID are required",
		)
	}

	return credentials, nil
}

func telegramBackupCredentialsPresent() bool {
	_, err := loadTelegramBackupCredentials()

	return err == nil
}

func wakeTelegramBackupScheduler() {
	select {
	case telegramBackupWake <- struct{}{}:
	default:
	}
}

func setTelegramBackupResult(
	runErr error,
) {
	cfg, err := loadTelegramBackupConfig()
	if err != nil {
		return
	}

	now := time.Now()

	cfg.LastRun = &now

	if runErr == nil {
		cfg.LastSuccess = &now
		cfg.LastError = ""
	} else {
		cfg.LastError = runErr.Error()
	}

	if cfg.Enabled {
		if duration, durationErr :=
			telegramBackupDuration(cfg); durationErr == nil {

			next := now.Add(duration)
			cfg.NextRun = &next
		}
	} else {
		cfg.NextRun = nil
	}

	_ = saveTelegramBackupConfig(cfg)

	wakeTelegramBackupScheduler()
}

func createTelegramScheduledBackup() (
	string,
	error,
) {
	/*
		با Scheduled Backup معمولی lock مشترک داریم
		تا دو Snapshot هم‌زمان ساخته نشوند.
	*/
	backupExecutionMu.Lock()
	defer backupExecutionMu.Unlock()

	sourceBackup, err := runBackupV2Export()
	if err != nil {
		return "", err
	}

	if err := validateBackupV2(
		sourceBackup,
	); err != nil {

		_ = os.Remove(sourceBackup)

		return "", fmt.Errorf(
			"Backup v2 validation failed: %w",
			err,
		)
	}

	backupDir := scheduledBackupDir()

	if err := os.MkdirAll(
		backupDir,
		0700,
	); err != nil {

		return "", err
	}

	now := time.Now()

	filename := fmt.Sprintf(
		"backup_%s_%s_%d.tar.gz",
		telegramBackupScheduleID,
		now.Format("20060102_150405"),
		now.UnixNano(),
	)

	destination := filepath.Join(
		backupDir,
		filename,
	)

	if err := moveBackupFile(
		sourceBackup,
		destination,
	); err != nil {

		return "", err
	}

	cfg, err := loadTelegramBackupConfig()
	if err == nil {
		cleanupOldBackups(
			&BackupSchedule{
				ID:            telegramBackupScheduleID,
				RetentionDays: cfg.RetentionDays,
			},
		)
	}

	return destination, nil
}

func sendTelegramBackupDocument(
	credentials telegramBackupCredentials,
	backupPath string,
) error {
	info, err := os.Stat(backupPath)
	if err != nil {
		return err
	}

	hostname, _ := os.Hostname()

	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)

	go func() {
		var writeErr error

		defer func() {
			if writeErr != nil {
				_ = writer.CloseWithError(
					writeErr,
				)

				return
			}

			if err := multipartWriter.Close(); err != nil {
				_ = writer.CloseWithError(err)

				return
			}

			_ = writer.Close()
		}()

		if err := multipartWriter.WriteField(
			"chat_id",
			credentials.ChatID,
		); err != nil {

			writeErr = err
			return
		}

		caption := fmt.Sprintf(
			"IranGate Backup v2\nServer: %s\nCreated: %s\nSize: %.2f MB",
			hostname,
			time.Now().Format(
				"2006-01-02 15:04:05 MST",
			),
			float64(info.Size())/
				1024/
				1024,
		)

		if err := multipartWriter.WriteField(
			"caption",
			caption,
		); err != nil {

			writeErr = err
			return
		}

		part, err := multipartWriter.CreateFormFile(
			"document",
			filepath.Base(
				backupPath,
			),
		)

		if err != nil {
			writeErr = err
			return
		}

		file, err := os.Open(
			backupPath,
		)

		if err != nil {
			writeErr = err
			return
		}

		defer file.Close()

		if _, err := io.Copy(
			part,
			file,
		); err != nil {

			writeErr = err
			return
		}
	}()

	endpoint := fmt.Sprintf(
		"https://api.telegram.org/bot%s/sendDocument",
		credentials.BotToken,
	)

	request, err := http.NewRequest(
		http.MethodPost,
		endpoint,
		reader,
	)

	if err != nil {
		return fmt.Errorf(
			"failed to create Telegram request",
		)
	}

	request.Header.Set(
		"Content-Type",
		multipartWriter.FormDataContentType(),
	)

	client := &http.Client{
		Timeout: 15 * time.Minute,
	}

	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf(
			"Telegram network request failed",
		)
	}

	defer response.Body.Close()

	body, err := io.ReadAll(
		io.LimitReader(
			response.Body,
			1024*1024,
		),
	)

	if err != nil {
		return fmt.Errorf(
			"failed to read Telegram response",
		)
	}

	var telegramResponse struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}

	if err := json.Unmarshal(
		body,
		&telegramResponse,
	); err != nil {

		return fmt.Errorf(
			"invalid Telegram API response",
		)
	}

	if response.StatusCode < 200 ||
		response.StatusCode >= 300 ||
		!telegramResponse.OK {

		description := strings.TrimSpace(
			telegramResponse.Description,
		)

		if description == "" {
			description = fmt.Sprintf(
				"HTTP %d",
				response.StatusCode,
			)
		}

		return fmt.Errorf(
			"Telegram API error: %s",
			description,
		)
	}

	return nil
}

func executeTelegramBackup(
	trigger string,
) error {
	select {
	case telegramBackupRunSemaphore <- struct{}{}:
		defer func() {
			<-telegramBackupRunSemaphore
		}()

	default:
		return fmt.Errorf(
			"Telegram backup is already running",
		)
	}

	fmt.Printf(
		"Telegram Backup: starting %s run\n",
		trigger,
	)

	credentials, err :=
		loadTelegramBackupCredentials()

	if err != nil {
		setTelegramBackupResult(err)
		return err
	}

	backupPath, err :=
		createTelegramScheduledBackup()

	if err != nil {
		setTelegramBackupResult(err)
		return err
	}

	/*
		حتی اگر Telegram خطا بدهد،
		backupPath روی سرور باقی می‌ماند.
	*/
	if err := sendTelegramBackupDocument(
		credentials,
		backupPath,
	); err != nil {

		setTelegramBackupResult(err)

		return err
	}

	setTelegramBackupResult(nil)

	fmt.Printf(
		"Telegram Backup: sent successfully: %s\n",
		backupPath,
	)

	return nil
}

func telegramBackupSchedulerLoop() {
	for {
		cfg, err := loadTelegramBackupConfig()

		if err != nil {
			select {
			case <-telegramBackupWake:
			case <-time.After(time.Minute):
			}

			continue
		}

		if !cfg.Enabled {
			select {
			case <-telegramBackupWake:
			case <-time.After(time.Minute):
			}

			continue
		}

		duration, err :=
			telegramBackupDuration(cfg)

		if err != nil {
			select {
			case <-telegramBackupWake:
			case <-time.After(time.Minute):
			}

			continue
		}

		if cfg.NextRun == nil {
			next := time.Now().Add(
				duration,
			)

			cfg.NextRun = &next

			_ = saveTelegramBackupConfig(
				cfg,
			)

			continue
		}

		wait := time.Until(
			*cfg.NextRun,
		)

		if wait <= 0 {
			if err := executeTelegramBackup(
				"scheduled",
			); err != nil {

				fmt.Printf(
					"Telegram Backup scheduled run failed: %v\n",
					err,
				)
			}

			continue
		}

		timer := time.NewTimer(wait)

		select {
		case <-timer.C:
			if err := executeTelegramBackup(
				"scheduled",
			); err != nil {

				fmt.Printf(
					"Telegram Backup scheduled run failed: %v\n",
					err,
				)
			}

		case <-telegramBackupWake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
	}
}

func getTelegramBackupSettingsHandler(
	w http.ResponseWriter,
	r *http.Request,
) {
	cfg, err := loadTelegramBackupConfig()

	if err != nil {
		sendError(
			w,
			"Failed to load Telegram backup settings",
			http.StatusInternalServerError,
		)

		return
	}

	cfg.StoragePath = scheduledBackupDir()
	cfg.CredentialsPresent =
		telegramBackupCredentialsPresent()

	sendSuccess(
		w,
		"Telegram backup settings retrieved",
		cfg,
	)
}

func updateTelegramBackupSettingsHandler(
	w http.ResponseWriter,
	r *http.Request,
) {
	var request TelegramBackupScheduleConfig

	if err := json.NewDecoder(
		r.Body,
	).Decode(&request); err != nil {

		sendError(
			w,
			"Invalid request body",
			http.StatusBadRequest,
		)

		return
	}

	request.IntervalUnit =
		strings.ToLower(
			strings.TrimSpace(
				request.IntervalUnit,
			),
		)

	if request.RetentionDays <= 0 {
		request.RetentionDays = 30
	}

	if request.RetentionDays > 365 {
		sendError(
			w,
			"Retention days cannot exceed 365",
			http.StatusBadRequest,
		)

		return
	}

	duration, err :=
		telegramBackupDuration(request)

	if err != nil {
		sendError(
			w,
			err.Error(),
			http.StatusBadRequest,
		)

		return
	}

	if request.Enabled {
		if _, err :=
			loadTelegramBackupCredentials(); err != nil {

			sendError(
				w,
				err.Error(),
				http.StatusBadRequest,
			)

			return
		}
	}

	current, _ :=
		loadTelegramBackupConfig()

	request.LastRun = current.LastRun
	request.LastSuccess = current.LastSuccess
	request.LastError = current.LastError
	request.StoragePath = scheduledBackupDir()

	if request.Enabled {
		next := time.Now().Add(
			duration,
		)

		request.NextRun = &next
	} else {
		request.NextRun = nil
	}

	if err := saveTelegramBackupConfig(
		request,
	); err != nil {

		sendError(
			w,
			"Failed to save Telegram backup settings",
			http.StatusInternalServerError,
		)

		return
	}

	wakeTelegramBackupScheduler()

	request.CredentialsPresent =
		telegramBackupCredentialsPresent()

	sendSuccess(
		w,
		"Telegram backup schedule saved",
		request,
	)
}

func runTelegramBackupNowHandler(
	w http.ResponseWriter,
	r *http.Request,
) {
	if _, err :=
		loadTelegramBackupCredentials(); err != nil {

		sendError(
			w,
			err.Error(),
			http.StatusBadRequest,
		)

		return
	}

	select {
	case telegramBackupRunSemaphore <- struct{}{}:
		<-telegramBackupRunSemaphore

	default:
		sendError(
			w,
			"Telegram backup is already running",
			http.StatusConflict,
		)

		return
	}

	go func() {
		if err := executeTelegramBackup(
			"manual",
		); err != nil {

			fmt.Printf(
				"Telegram Backup manual run failed: %v\n",
				err,
			)
		}
	}()

	sendSuccess(
		w,
		"Telegram backup started",
		map[string]interface{}{
			"status": "started",
		},
	)
}

func init() {
	go telegramBackupSchedulerLoop()
}
