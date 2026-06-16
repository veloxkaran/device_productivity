package registration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"my-monitor/machine"
	"my-monitor/storage"
	"net/http"
	"time"
)

type Config struct {
	URL       string
	Token     string // Candidate Passport Bearer token
	CompanyID int64
}

type statusResp struct {
	Status string `json:"status"`
	Data   struct {
		UUID       string `json:"uuid"`
		Status     string `json:"status"`
		LastSyncAt string `json:"last_sync_at"`
	} `json:"data"`
}

// Register sends machine info to Hajir and persists the result locally.
// Safe to call repeatedly — Hajir upserts on machine_id.
func Register(cfg Config, db *storage.DB, info machine.Info) error {
	payload := map[string]interface{}{
		"machine_id":   info.MachineID,
		"company_id":   cfg.CompanyID,
		"machine_name": info.MachineName,
		"os":           info.OS,
		"os_version":   info.OSVersion,
		"app_version":  info.AppVersion,
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", cfg.URL+"/api/v2/productivity/device/register", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.Token)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("registration request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("registration failed (%d): %s", resp.StatusCode, raw)
	}

	var sr statusResp
	if err := json.Unmarshal(raw, &sr); err != nil {
		return fmt.Errorf("invalid registration response: %w", err)
	}

	return db.SaveDeviceRegistration(storage.DeviceRegistration{
		DeviceUUID:   sr.Data.UUID,
		MachineID:    info.MachineID,
		CompanyID:    cfg.CompanyID,
		Status:       sr.Data.Status,
		RegisteredAt: time.Now(),
	})
}

// PollStatus checks the current approval status from Hajir and updates local DB.
// Returns the status string: "pending" | "approved" | "blocked"
func PollStatus(cfg Config, db *storage.DB, machineID string) (string, error) {
	req, err := http.NewRequest("GET", cfg.URL+"/api/v2/productivity/device/status", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("X-Machine-ID", machineID)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("status poll failed: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status poll failed (%d): %s", resp.StatusCode, raw)
	}

	var sr statusResp
	if err := json.Unmarshal(raw, &sr); err != nil {
		return "", err
	}

	reg, _ := db.GetDeviceRegistration()
	if reg != nil && reg.Status != sr.Data.Status {
		reg.Status = sr.Data.Status
		_ = db.SaveDeviceRegistration(*reg)
		log.Printf("registration: status changed → %s", sr.Data.Status)
	}

	return sr.Data.Status, nil
}
