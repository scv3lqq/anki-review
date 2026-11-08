package provisioner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallerBinaryLayouts(t *testing.T) {
	contents, err := os.ReadFile("../../deploy/install-multi-account.sh")
	if err != nil {
		t.Fatal(err)
	}
	source := string(contents)
	start := strings.Index(source, "binary=")
	end := strings.Index(source, "# Preflight the HTTP recovery key")
	if start < 0 || end <= start {
		t.Fatal("cannot locate installer binary selection before recovery preflight")
	}
	selection := source[start:end]
	for _, layout := range []string{"source", "kit", "missing"} {
		t.Run(layout, func(t *testing.T) {
			root := t.TempDir()
			directory := filepath.Join(root, "deploy")
			if layout == "kit" {
				directory = filepath.Join(root, "kit")
			}
			if err := os.MkdirAll(directory, 0700); err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(directory, "bin", "anki-review-provisioner-linux-amd64")
			if layout == "source" {
				if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(root, "cmd", "anki-review-provisioner"), 0700); err != nil {
					t.Fatal(err)
				}
				want = filepath.Join(root, "bin", "anki-review-provisioner-linux-amd64")
			}
			if layout != "missing" {
				if err := os.MkdirAll(filepath.Dir(want), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(want, []byte("fixture"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			program := "set -eu\nfail(){ exit 19; }\nscript_dir=$1\n" + selection + "\nprintf '%s' \"$binary\""
			command := exec.CommandContext(context.Background(), "bash", "-c", program, "--", directory)
			output, err := command.Output()
			if layout == "missing" {
				if err == nil {
					t.Fatal("missing binary accepted")
				}
				return
			}
			if err != nil || filepath.Clean(string(output)) != want {
				t.Fatalf("selected %q, want %q: %v", output, want, err)
			}
		})
	}
}

func TestInstallerRetainsValidatedConfigWithoutEvaluatingIt(t *testing.T) {
	contents, err := os.ReadFile("../../deploy/install-multi-account.sh")
	if err != nil {
		t.Fatal(err)
	}
	source := string(contents)
	start := strings.Index(source, "validate_settings()")
	end := strings.Index(source[start:], "[[ $(id -u")
	if start < 0 || end < 0 {
		t.Fatal("cannot locate installer config parser")
	}
	parser := strings.ReplaceAll(source[start:start+end], "/etc/anki-review-provisioner.conf", `"$test_config"`)
	for _, test := range []struct {
		name, body, override, want string
		reject                     bool
	}{
		{name: "preserve", body: "ANKI_UID=1234\nANKI_GID=1235\nMAX_GUESTS=5\nSTORAGE_BYTES=4294967296\n", want: "1234:1235:5:4294967296"},
		{name: "explicit override", body: "ANKI_UID=1234\nANKI_GID=1235\nMAX_GUESTS=5\nSTORAGE_BYTES=4294967296\n", override: "MAX_GUESTS=2", want: "1234:1235:2:4294967296"},
		{name: "reject explicit empty", body: "ANKI_UID=1234\nANKI_GID=1235\nMAX_GUESTS=5\nSTORAGE_BYTES=4294967296\n", override: "MAX_GUESTS=", reject: true},
		{name: "reject partial", body: "ANKI_UID=1234\n", reject: true},
		{name: "reject duplicate", body: "ANKI_UID=1234\nANKI_UID=1236\nANKI_GID=1235\nMAX_GUESTS=5\nSTORAGE_BYTES=4294967296\n", reject: true},
		{name: "reject shell", body: "ANKI_UID=$(touch PROBE_SENTINEL)\nANKI_GID=1235\nMAX_GUESTS=5\nSTORAGE_BYTES=4294967296\n", reject: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			config := filepath.Join(directory, "daemon.conf")
			attack := filepath.Join(directory, "evaluated")
			if err := os.WriteFile(config, []byte(strings.ReplaceAll(test.body, "PROBE_SENTINEL", attack)), 0600); err != nil {
				t.Fatal(err)
			}
			program := "set -eu\nfail(){ exit 19; }\nstat(){ printf '0:600'; }\ntest_config=$1\n" + parser + "\nprintf '%s:%s:%s:%s' \"$anki_uid\" \"$anki_gid\" \"$max_guests\" \"$storage_bytes\""
			command := exec.CommandContext(context.Background(), "bash", "-c", program, "--", config)
			command.Env = []string{"PATH=" + os.Getenv("PATH")}
			if test.override != "" {
				command.Env = append(command.Env, test.override)
			}
			output, err := command.Output()
			if _, statErr := os.Stat(attack); !os.IsNotExist(statErr) {
				t.Fatal("evaluated saved configuration")
			}
			if test.reject {
				if err == nil {
					t.Fatalf("accepted invalid saved config: %s", output)
				}
				return
			}
			if err != nil || string(output) != test.want {
				t.Fatalf("configuration=%q want=%q: %v", output, test.want, err)
			}
		})
	}
}

func TestDeploymentPreservesHostMountsAndHTTPIsolation(t *testing.T) {
	unit, err := os.ReadFile("../../deploy/systemd/anki-review-provisioner.service")
	if err != nil {
		t.Fatal(err)
	}
	text := string(unit)
	for _, required := range []string{"User=root", "Group=root", "PrivateMounts=no", "--root /var/lib/anki-review-provisioner", "--socket /run/anki-review-provisioner/control.sock", "--group anki-review"} {
		if !strings.Contains(text, required) {
			t.Errorf("missing %q", required)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		for _, forbidden := range []string{"ProtectSystem=", "PrivateTmp=", "PrivateDevices=", "ProtectKernel", "ProtectControlGroups=", "ReadWritePaths=", "ReadOnlyPaths=", "InaccessiblePaths="} {
			if strings.HasPrefix(line, forbidden) {
				t.Errorf("mount namespace directive hides storage from dockerd: %s", line)
			}
		}
	}
	installer, err := os.ReadFile("../../deploy/install-multi-account.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"usermod -a -G anki-review", "-g root -m 0700 /var/lib/anki-review-provisioner", "-g anki-review -m 0750 /run/anki-review-provisioner", "systemctl enable anki-review-provisioner.service", "systemctl restart anki-review-provisioner.service"} {
		if !strings.Contains(string(installer), required) {
			t.Errorf("missing installer isolation: %q", required)
		}
	}
	for _, forbidden := range []string{"-G docker", "restart anki-review.service", "restart anki-review\n", "chmod 666", "chmod 0666"} {
		if strings.Contains(string(installer), forbidden) {
			t.Errorf("unsafe installation: %q", forbidden)
		}
	}
	for _, required := range []string{"runuser -u", "--entrypoint /usr/bin/id", "DOCKER_HOST=unix:///var/run/docker.sock", "pinned image UID/GID"} {
		if !strings.Contains(string(installer), required) {
			t.Errorf("missing installation prerequisite: %q", required)
		}
	}
	for _, required := range []string{"systemctl stop anki-review-provisioner.service", "container update --restart=no", "io.anki-review.managed=provisioner-v1", "io.anki-review.account", "io.anki-review.reservation", "saved_anki_uid", "saved_anki_gid", "saved_max_guests", "saved_storage_bytes"} {
		if !strings.Contains(string(installer), required) {
			t.Errorf("missing safe update: %s", required)
		}
	}
	if strings.Index(string(installer), "systemctl stop anki-review-provisioner.service") > strings.Index(string(installer), "install -o root -g root -m 0755") {
		t.Error("daemon binary replaced before stopping the daemon")
	}
}
