package provisioner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (s *Service) docker(ctx context.Context, args ...string) ([]byte, error) {
	result, err := s.runner.Run(ctx, "docker", args...)
	if err != nil {
		return nil, fmt.Errorf("docker %s %s: %w", args[0], args[1], err)
	}
	return result, nil
}

func (s *Service) labels(record reservation) []string {
	return []string{"--label", managedLabel + "=" + managerValue, "--label", accountLabel + "=" + record.ID, "--label", reservationLabel + "=" + record.Name}
}

func (s *Service) owned(ctx context.Context, kind string, record reservation) (bool, error) {
	format := "{{.Name}}"
	if kind == "container" {
		format = "{{.Names}}"
	}
	args := []string{kind, "ls"}
	if kind == "container" {
		args = append(args, "--all")
	}
	args = append(args, "--filter", "name="+record.Name, "--format", format)
	list, err := s.docker(ctx, args...)
	if err != nil {
		return false, err
	}
	found := false
	for _, name := range strings.Fields(string(list)) {
		if name == record.Name {
			found = true
			break
		}
	}
	if !found {
		return false, nil
	}
	data, err := s.docker(ctx, kind, "inspect", record.Name)
	if err != nil {
		return false, err
	}
	var entries []struct {
		Labels  map[string]string `json:"Labels"`
		Driver  string            `json:"Driver"`
		Options map[string]string `json:"Options"`
		Config  struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return false, fmt.Errorf("decode docker ownership: %w", err)
	}
	if len(entries) != 1 {
		return false, errors.New("provisioner: ambiguous resource ownership")
	}
	labels := entries[0].Labels
	if kind == "container" {
		labels = entries[0].Config.Labels
	}
	if labels[managedLabel] != managerValue || labels[accountLabel] != record.ID || labels[reservationLabel] != record.Name {
		return false, errors.New("provisioner: refusing foreign Docker resource")
	}
	if kind == "volume" {
		volume := entries[0]
		if volume.Driver != "local" || len(volume.Options) != 3 || volume.Options["type"] != "none" || volume.Options["o"] != "bind" || volume.Options["device"] != filepath.Join(s.directory(record), "mount", "data") {
			return false, errors.New("provisioner: refusing volume outside the bounded filesystem")
		}
	}
	return true, nil
}

func (s *Service) ensureResources(ctx context.Context, record reservation, rebind bool) error {
	// Check the whole existing set before any mutation, including a colliding container.
	existing := make(map[string]bool)
	for _, kind := range []string{"network", "volume", "container"} {
		exists, err := s.owned(ctx, kind, record)
		if err != nil {
			return err
		}
		existing[kind] = exists
	}
	if existing["container"] {
		if _, err := s.docker(ctx, "container", "update", "--restart=no", record.Name); err != nil {
			return err
		}
		if rebind {
			if _, err := s.docker(ctx, "container", "stop", "--time", "60", record.Name); err != nil {
				return err
			}
			if _, err := s.docker(ctx, "container", "rm", record.Name); err != nil {
				return err
			}
			existing["container"] = false
		}
	}
	if !existing["network"] {
		args := append([]string{"network", "create", "--driver", "bridge"}, s.labels(record)...)
		args = append(args, record.Name)
		if _, err := s.docker(ctx, args...); err != nil {
			return err
		}
	}
	if !existing["volume"] {
		args := append([]string{"volume", "create", "--driver", "local", "--opt", "type=none", "--opt", "o=bind", "--opt", "device=" + filepath.Join(s.directory(record), "mount", "data")}, s.labels(record)...)
		args = append(args, record.Name)
		if _, err := s.docker(ctx, args...); err != nil {
			return err
		}
	}
	if !existing["container"] {
		args := s.containerArgs(record)
		if _, err := s.docker(ctx, args...); err != nil {
			return err
		}
	}
	if _, err := s.docker(ctx, "container", "start", record.Name); err != nil {
		return err
	}
	return nil
}

func (s *Service) containerArgs(record reservation) []string {
	identity := strconv.Itoa(s.config.UID) + ":" + strconv.Itoa(s.config.GID)
	args := []string{"container", "create", "--name", record.Name, "--network", record.Name, "--user", identity,
		"--init", "--restart", "no", "--stop-timeout", "60", "--memory", "2g", "--memory-swap", "2g", "--cpus", "1", "--pids-limit", "1024",
		"--read-only", "--shm-size", "256m", "--tmpfs", "/tmp:size=64m,mode=1777", "--tmpfs", fmt.Sprintf("/home/anki:size=256m,uid=%d,gid=%d,mode=0700", s.config.UID, s.config.GID),
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--publish", fmt.Sprintf("127.0.0.1:%d:8765", record.Port),
		"--mount", "type=volume,source=" + record.Name + ",target=/data",
		"--mount", "type=bind,source=" + filepath.Join(s.directory(record), "ankiconnect_key") + ",target=/run/secrets/ankiconnect_key,readonly",
		"--mount", "type=bind,source=" + filepath.Join(s.directory(record), "vnc_password") + ",target=/run/secrets/vnc_password,readonly",
		"--env", "QTWEBENGINE_CHROMIUM_FLAGS=--disable-gpu --js-flags=--max-old-space-size=128",
		"--log-driver", "json-file", "--log-opt", "max-size=10m", "--log-opt", "max-file=3"}
	args = append(args, s.labels(record)...)
	return append(args, Image)
}

func waitReady(ctx context.Context, endpoint Endpoint) error {
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	body, err := json.Marshal(struct {
		Action  string `json:"action"`
		Version int    `json:"version"`
		Key     string `json:"key"`
	}{"version", 6, endpoint.Key})
	if err != nil {
		return fmt.Errorf("encode readiness: %w", err)
	}
	for {
		if err := probeReady(ctx, client, endpoint.URL, body); err == nil {
			return nil
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("wait for AnkiConnect v6: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func probeReady(ctx context.Context, client *http.Client, url string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create readiness request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("probe readiness: %w", err)
	}
	defer response.Body.Close()
	var result struct {
		Result int             `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, response.Body, 4096)).Decode(&result); err != nil {
		return fmt.Errorf("decode readiness: %w", err)
	}
	if response.StatusCode != http.StatusOK || result.Result != 6 || string(result.Error) != "null" {
		return errors.New("AnkiConnect v6 not ready")
	}
	return nil
}
