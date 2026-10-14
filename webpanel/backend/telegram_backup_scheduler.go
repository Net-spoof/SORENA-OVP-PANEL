package main

import (
	"bytes"
	"encoding/json"
	"errors"
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
	// Ephemeral run status, populated only by the GET handler (not persisted).
	Running            bool       `json:"running,omitempty"`
	RunID              string     `json:"run_id,omitempty"`
	CompletedRunID     string     `json:"completed_run_id,omitempty"`
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

	telegramBackupBusy = errors.New("Telegram backup is already running")

	telegramBackupStatusMu sync.RWMutex
	telegramBackupRunning bool
	telegramBackupRunID string
	telegramBackupCompletedRunID string
)

// Run IDs let the UI verify completion of the specific manual request.
// This state is in-memory only; it never enters backup configuration files.
func beginTelegramBackupRun() string {
	id := fmt.Sprintf("%d", time.Now().UnixNano())
	telegramBackupStatusMu.Lock()
	telegramBackupRunID = id
	telegramBackupRunning = true
	telegramBackupStatusMu.Unlock()
	return id
}

func finishTelegramBackupRun(id string) {
	telegramBackupStatusMu.Lock()
	defer telegramBackupStatusMu.Unlock()
	if telegramBackupRunID == id {
		telegramBackupRunning = false
		telegramBackupCompletedRunID = id
	}
}

func readTelegramBackupRunStatus() (bool, string, string) {
	telegramBackupStatusMu.RLock()
	defer telegramBackupStatusMu.RUnlock()
	return telegramBackupRunning, telegramBackupRunID, telegramBackupCompletedRunID
}

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

const telegramBackupMaxPartBytes int64 = 45 * 1024 * 1024

// Telegram expects an explicit Content-Length for reliable multipart uploads
// through proxies. A dry writer computes the exact framing bytes without
// buffering the potentially large archive part in memory.
func telegramBackupMultipartOverhead(
    boundary, chatID, caption, uploadName string,
) (int64, error) {
    var framing bytes.Buffer
    probe := multipart.NewWriter(&framing)
    if err := probe.SetBoundary(boundary); err != nil {
        return 0, err
    }
    if err := probe.WriteField("chat_id", chatID); err != nil {
        return 0, err
    }
    if err := probe.WriteField("caption", caption); err != nil {
        return 0, err
    }
    if _, err := probe.CreateFormFile("document", uploadName); err != nil {
        return 0, err
    }
    if err := probe.Close(); err != nil {
        return 0, err
    }
    return int64(framing.Len()), nil
}

func sendTelegramBackupPartOnce(
	credentials telegramBackupCredentials,
	backupPath string,
	offset int64,
	length int64,
	uploadName string,
	caption string,
) error {
	reader, writer := io.Pipe()
	// An HTTP failure must also unblock the multipart producer goroutine.
	defer reader.CloseWithError(io.ErrClosedPipe)
	multipartWriter := multipart.NewWriter(writer)

	go func() {
		var writeErr error

		defer func() {
			if writeErr != nil {
				_ = writer.CloseWithError(writeErr)
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

		if err := multipartWriter.WriteField(
			"caption",
			caption,
		); err != nil {
			writeErr = err
			return
		}

		part, err := multipartWriter.CreateFormFile(
			"document",
			uploadName,
		)
		if err != nil {
			writeErr = err
			return
		}

		file, err := os.Open(backupPath)
		if err != nil {
			writeErr = err
			return
		}
		defer file.Close()

		section := io.NewSectionReader(
			file,
			offset,
			length,
		)

		if _, err := io.Copy(part, section); err != nil {
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
			"failed to create Telegram request: %w",
			err,
		)
	}

	request.Header.Set(
		"Content-Type",
		multipartWriter.FormDataContentType(),
	)

	overhead, err := telegramBackupMultipartOverhead(
		multipartWriter.Boundary(), credentials.ChatID, caption, uploadName,
	)
	if err != nil {
		return fmt.Errorf("failed to prepare Telegram multipart upload: %w", err)
	}
	request.ContentLength = overhead + length

	client := &http.Client{
		Timeout: 15 * time.Minute,
	}

	response, err := client.Do(request)
	if err != nil {
		return &telegramBackupRetryableError{err: fmt.Errorf(
			"Telegram network request failed: %w", err,
		)}
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
			"failed to read Telegram response: %w",
			err,
		)
	}

	var telegramResponse struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Parameters struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}

	if err := json.Unmarshal(
		body,
		&telegramResponse,
	); err != nil {
		apiErr := fmt.Errorf(
			"invalid Telegram API response (HTTP %d): %w",
			response.StatusCode,
			err,
		)
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
			return &telegramBackupRetryableError{err: apiErr}
		}
		return apiErr
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

		apiErr := fmt.Errorf("Telegram API error (HTTP %d): %s",
			response.StatusCode, description)
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
			return &telegramBackupRetryableError{
				err: apiErr,
				retryAfter: time.Duration(telegramResponse.Parameters.RetryAfter) * time.Second,
			}
		}
		return apiErr
	}

	return nil
}

// Retry transient Telegram API failures without recreating the backup archive.
type telegramBackupRetryableError struct {
	err error
	retryAfter time.Duration
}

func (e *telegramBackupRetryableError) Error() string { return e.err.Error() }

func sendTelegramBackupPart(
	credentials telegramBackupCredentials,
	backupPath string,
	offset int64,
	length int64,
	uploadName string,
	caption string,
) error {
	for attempt := 0; attempt < 3; attempt++ {
		err := sendTelegramBackupPartOnce(
			credentials, backupPath, offset, length, uploadName, caption,
		)
		if err == nil {
			return nil
		}
		var temporary *telegramBackupRetryableError
		if !errors.As(err, &temporary) || attempt == 2 {
			return err
		}
		delay := time.Duration(2 << attempt) * time.Second
		if temporary.retryAfter > delay {
			delay = temporary.retryAfter
		}
		if delay > 5*time.Minute {
			delay = 5*time.Minute
		}
		fmt.Printf("Telegram Backup: temporary send error, retry %d/2 after %s: %v\n",
			attempt+1, delay, err)
		time.Sleep(delay)
	}
	return fmt.Errorf("Telegram backup upload retries exhausted")
}

// Telegram-facing branding is independent of the backup archive format.
// Keep internal archive names unchanged for scheduler/restore compatibility.
const telegramBackupTitle = "🛡️ SURENA Backup V2"

func telegramBackupCaption(hostname, createdAt string, totalSizeMB float64, part, totalParts int) string {
	if totalParts > 1 {
		return fmt.Sprintf(
			"%s\nServer: %s\nCreated: %s\nTotal size: %.2f MB\nPart: %d/%d\n\nDownload all parts and concatenate them in order to restore the original .tar.gz archive.",
			telegramBackupTitle, hostname, createdAt, totalSizeMB, part, totalParts,
		)
	}
	return fmt.Sprintf(
		"%s\nServer: %s\nCreated: %s\nSize: %.2f MB",
		telegramBackupTitle, hostname, createdAt, totalSizeMB,
	)
}

func telegramBackupReassemblyMessage(totalParts int, backupName string) string {
	return fmt.Sprintf(
		"✅ SURENA Backup V2 sent in %d parts.\n\nReassemble on Linux:\ncat '%s.part'* > '%s'",
		totalParts, backupName, backupName,
	)
}

func sendTelegramBackupDocument(
	credentials telegramBackupCredentials,
	backupPath string,
) error {
	info, err := os.Stat(backupPath)
	if err != nil {
		return err
	}

	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return fmt.Errorf(
			"backup archive is missing or empty",
		)
	}

	hostname, _ := os.Hostname()
	backupName := filepath.Base(backupPath)
	totalSize := info.Size()
	totalSizeMB := float64(totalSize) / 1024 / 1024

	totalParts := int(
		(totalSize + telegramBackupMaxPartBytes - 1) /
			telegramBackupMaxPartBytes,
	)
	if totalParts < 1 {
		totalParts = 1
	}

	for partIndex := 0; partIndex < totalParts; partIndex++ {
		offset := int64(partIndex) * telegramBackupMaxPartBytes
		partLength := telegramBackupMaxPartBytes

		remaining := totalSize - offset
		if remaining < partLength {
			partLength = remaining
		}

		uploadName := backupName
		caption := telegramBackupCaption(
			hostname,
			time.Now().Format("2006-01-02 15:04:05 MST"),
			totalSizeMB,
			partIndex+1,
			totalParts,
		)

		if totalParts > 1 {
			uploadName = fmt.Sprintf("%s.part%03d", backupName, partIndex+1)
		}

		fmt.Printf(
			"Telegram Backup: sending %s (%d/%d, %.2f MB)\n",
			uploadName,
			partIndex+1,
			totalParts,
			float64(partLength)/1024/1024,
		)

		if err := sendTelegramBackupPart(
			credentials,
			backupPath,
			offset,
			partLength,
			uploadName,
			caption,
		); err != nil {
			return fmt.Errorf(
				"Telegram backup part %d/%d failed: %w",
				partIndex+1,
				totalParts,
				err,
			)
		}
	}

	if totalParts > 1 {
		instructions := telegramBackupReassemblyMessage(totalParts, backupName)

		if err := sendTelegramMessage(
			credentials.BotToken,
			credentials.ChatID,
			instructions,
		); err != nil {
			// The archive parts are already delivered. Do not mark the backup
			// as failed only because the optional reconstruction note failed.
			fmt.Printf(
				"Telegram Backup: reconstruction note failed: %v\n",
				err,
			)
		}
	}

	return nil
}

func executeTelegramBackupWithSlot(
	trigger string,
) error {
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

func executeTelegramBackup(
	trigger string,
) error {
	select {
	case telegramBackupRunSemaphore <- struct{}{}:
		runID := beginTelegramBackupRun()
		defer func() {
			finishTelegramBackupRun(runID)
			<-telegramBackupRunSemaphore
		}()
	default:
		return telegramBackupBusy
	}

	return executeTelegramBackupWithSlot(trigger)
}

func telegramBackupSchedulerLoop() {
	startup := true

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

		// After a webpanel restart, do not immediately "catch up" an overdue
		// Telegram backup. That behavior used to start a heavy export during
		// startup and race manual Export/Backup & Send Now requests.
		if startup {
			startup = false

			if cfg.NextRun == nil ||
				!cfg.NextRun.After(time.Now()) {

				next := time.Now().Add(duration)
				cfg.NextRun = &next

				_ = saveTelegramBackupConfig(cfg)
				continue
			}
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
				fmt.Printf("Telegram Backup scheduled run failed: %v\n", err)
				if errors.Is(err, telegramBackupBusy) {
					// Avoid a tight CPU loop while a manual upload is active.
					select {
					case <-telegramBackupWake:
					case <-time.After(30 * time.Second):
					}
				}
			}
			continue
		}

		timer := time.NewTimer(wait)

		select {
		case <-timer.C:
			if err := executeTelegramBackup(
				"scheduled",
			); err != nil {
				fmt.Printf("Telegram Backup scheduled run failed: %v\n", err)
				if errors.Is(err, telegramBackupBusy) {
					select {
					case <-telegramBackupWake:
					case <-time.After(30 * time.Second):
					}
				}
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
	cfg.Running, cfg.RunID, cfg.CompletedRunID = readTelegramBackupRunStatus()

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

	var runID string
	select {
	case telegramBackupRunSemaphore <- struct{}{}:
		// Slot stays held until the background run actually completes.
		runID = beginTelegramBackupRun()
	default:
		sendError(
			w,
			"Telegram backup is already running",
			http.StatusConflict,
		)
		return
	}

	go func() {
		defer func() {
			finishTelegramBackupRun(runID)
			<-telegramBackupRunSemaphore
		}()

		if err := executeTelegramBackupWithSlot(
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
			"run_id": runID,
		},
	)
}

func init() {
	go telegramBackupSchedulerLoop()
}
