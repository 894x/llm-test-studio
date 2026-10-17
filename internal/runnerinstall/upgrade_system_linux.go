package runnerinstall

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os/user"
	"slices"
	"strconv"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerconfig"
)

type localUpgradeSystem struct{ localSystem }

func (system localUpgradeSystem) Account(ctx context.Context) (account, error) {
	// Upgrade must never manufacture an installation identity or service account.
	if _, err := user.Lookup(serviceUser); err != nil {
		return account{}, ErrUnownedPath
	}
	return system.localSystem.Account(ctx)
}

func (system localUpgradeSystem) Stop(ctx context.Context) error {
	if _, err := command(ctx, "systemctl", "stop", ServiceName); err != nil {
		return ErrService
	}
	return nil
}

func applyUpgradeMaintenance(
	ctx context.Context,
	config runnerconfig.Config,
	token []byte,
	command jobcontract.MaintenanceCommand,
) (jobhost.MaintenanceResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	document, err := json.Marshal(command)
	if err != nil {
		return jobhost.MaintenanceResult{}, ErrInvalid
	}
	transport := &http.Transport{DialContext: (&net.Dialer{Timeout: time.Second}).DialContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport, Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	url := "http://127.0.0.1:" + strconv.Itoa(int(config.ListenPort)) + "/runner/maintenance"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(document))
	if err != nil {
		return jobhost.MaintenanceResult{}, ErrInvalid
	}
	request.Header.Set("Authorization", "Bearer "+string(token))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(jobcontract.ContractHeader, strconv.Itoa(jobcontract.CurrentContract))
	response, err := client.Do(request)
	if err != nil {
		return jobhost.MaintenanceResult{}, ErrService
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusConflict {
		data, err := io.ReadAll(io.LimitReader(response.Body, MaxRecordBytes+1))
		if err != nil || len(data) > MaxRecordBytes {
			return jobhost.MaintenanceResult{}, ErrIntegrity
		}
		return jobhost.MaintenanceResult{}, maintenanceConflict(data)
	}
	if response.StatusCode != http.StatusOK {
		return jobhost.MaintenanceResult{}, ErrService
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxRecordBytes+1))
	if err != nil || len(data) > MaxRecordBytes {
		return jobhost.MaintenanceResult{}, ErrIntegrity
	}
	var result jobhost.MaintenanceResult
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return jobhost.MaintenanceResult{}, ErrIntegrity
	}
	if _, err := decoder.Token(); err != io.EOF {
		return jobhost.MaintenanceResult{}, ErrIntegrity
	}
	if result.Record.Validate(config.InstallationID) != nil || result.Record.OperationID != command.OperationID {
		return jobhost.MaintenanceResult{}, ErrIntegrity
	}
	paused := command.Action == jobcontract.MaintenancePause
	validGate := result.Record.Paused == paused && result.Status.Healthy && result.Status.Ready != paused
	if paused {
		validGate = validGate && result.Status.MaintenanceOperationID == command.OperationID
	} else {
		validGate = validGate && result.Status.MaintenanceOperationID == ""
	}
	if !validGate {
		return jobhost.MaintenanceResult{}, ErrIntegrity
	}
	return result, nil
}

func maintenanceConflict(document []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return ErrIntegrity
	}
	required := []string{"contract", "code", "message", "retryable"}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		allowed := slices.Contains(required, name) || name == "field"
		validName := err == nil && ok && allowed
		if !validName || fields[name] != nil {
			return ErrIntegrity
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return ErrIntegrity
		}
		fields[name] = value
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return ErrIntegrity
	}
	if decoder.Decode(new(any)) != io.EOF {
		return ErrIntegrity
	}
	for _, name := range required {
		if fields[name] == nil {
			return ErrIntegrity
		}
	}
	var wire struct {
		Contract  int    `json:"contract"`
		Code      string `json:"code"`
		Message   string `json:"message"`
		Field     string `json:"field"`
		Retryable bool   `json:"retryable"`
	}
	if json.Unmarshal(document, &wire) != nil || wire.Contract != jobcontract.CurrentContract {
		return ErrIntegrity
	}
	if wire.Code == "upload_in_progress" && wire.Retryable {
		return ErrBusy
	}
	if wire.Code == "job_conflict" && !wire.Retryable {
		return ErrConflict
	}
	return ErrIntegrity
}
