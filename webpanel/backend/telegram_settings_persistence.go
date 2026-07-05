package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type telegramSettingsPersistentPayload struct {
	BotToken     string `json:"bot_token"`
	ChatID       string `json:"chat_id"`
	Enabled      bool   `json:"enabled"`
	ExpiringDays int    `json:"expiring_days"`
}

func defaultPersistentTelegramSettings() telegramSettingsPersistentPayload {
	return telegramSettingsPersistentPayload{
		ExpiringDays: 7,
	}
}

func readPersistentTelegramSettings() (
	telegramSettingsPersistentPayload,
	error,
) {
	settings := defaultPersistentTelegramSettings()

	data, err := os.ReadFile(
		TelegramDBPath,
	)

	if os.IsNotExist(err) {
		return settings, nil
	}

	if err != nil {
		return settings, err
	}

	if strings.TrimSpace(
		string(data),
	) == "" {

		return settings, nil
	}

	if err := json.Unmarshal(
		data,
		&settings,
	); err != nil {

		return settings, err
	}

	if settings.ExpiringDays < 1 ||
		settings.ExpiringDays > 30 {

		settings.ExpiringDays = 7
	}

	return settings, nil
}

func writePersistentTelegramSettings(
	settings telegramSettingsPersistentPayload,
) error {
	if err := os.MkdirAll(
		filepath.Dir(
			TelegramDBPath,
		),
		0700,
	); err != nil {

		return err
	}

	data, err := json.MarshalIndent(
		settings,
		"",
		"  ",
	)

	if err != nil {
		return err
	}

	tempPath := TelegramDBPath + ".tmp"

	if err := os.WriteFile(
		tempPath,
		data,
		0600,
	); err != nil {

		return err
	}

	if err := os.Rename(
		tempPath,
		TelegramDBPath,
	); err != nil {

		_ = os.Remove(
			tempPath,
		)

		return err
	}

	return os.Chmod(
		TelegramDBPath,
		0600,
	)
}

func getTelegramSettingsPersistentHandler(
	w http.ResponseWriter,
	r *http.Request,
) {
	settings, err :=
		readPersistentTelegramSettings()

	if err != nil {
		sendError(
			w,
			"Failed to read Telegram settings",
			http.StatusInternalServerError,
		)

		return
	}

	sendSuccess(
		w,
		"Telegram settings retrieved",
		settings,
	)
}

func updateTelegramSettingsPersistentHandler(
	w http.ResponseWriter,
	r *http.Request,
) {
	var settings telegramSettingsPersistentPayload

	if err := json.NewDecoder(
		r.Body,
	).Decode(
		&settings,
	); err != nil {

		sendError(
			w,
			"Invalid request body",
			http.StatusBadRequest,
		)

		return
	}

	settings.BotToken = strings.TrimSpace(
		settings.BotToken,
	)

	settings.ChatID = strings.TrimSpace(
		settings.ChatID,
	)

	if settings.ExpiringDays == 0 {
		settings.ExpiringDays = 7
	}

	if settings.ExpiringDays < 1 ||
		settings.ExpiringDays > 30 {

		sendError(
			w,
			"Expiring days must be between 1 and 30",
			http.StatusBadRequest,
		)

		return
	}

	botPresent :=
		settings.BotToken != ""

	chatPresent :=
		settings.ChatID != ""

	if botPresent != chatPresent {
		sendError(
			w,
			"Bot Token and Chat ID must both be provided",
			http.StatusBadRequest,
		)

		return
	}

	if settings.Enabled &&
		(!botPresent || !chatPresent) {

		sendError(
			w,
			"Bot Token and Chat ID are required",
			http.StatusBadRequest,
		)

		return
	}

	if err := writePersistentTelegramSettings(
		settings,
	); err != nil {

		sendError(
			w,
			"Failed to save Telegram settings",
			http.StatusInternalServerError,
		)

		return
	}

	/*
		بعد از ذخیره credential،
		scheduler را بیدار می‌کنیم تا state جدید را ببیند.
	*/
	wakeTelegramBackupScheduler()

	sendSuccess(
		w,
		"Telegram settings updated",
		settings,
	)
}
