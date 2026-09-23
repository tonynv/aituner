package bench

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var pyVerRe = regexp.MustCompile(`^(\d+)\.(\d+)`)

// EnsureRuntime creates the aituner venv if missing and installs/updates mlx + mlx-lm to the latest stable.
// Idempotent: re-running upgrades in place. basePython must be >= 3.10 (mlx requirement, verified on PyPI).
func EnsureRuntime(ctx context.Context, emit Emit, dataDir, basePython, basePyVersion string) (MLX, error) {
	m := pyVerRe.FindStringSubmatch(basePyVersion)
	if m == nil {
		return MLX{}, errors.New("python version unknown; install Python 3.14 (brew install python@3.14)")
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	if maj < 3 || (maj == 3 && min < 10) {
		return MLX{}, fmt.Errorf("python %s is too old for MLX (needs >= 3.10)", basePyVersion)
	}
	venv := filepath.Join(dataDir, "venv")
	py := filepath.Join(venv, "bin", "python")
	if _, err := os.Stat(py); err != nil {
		emit(Event{Level: "info", Suite: "setup", Message: "creating Python environment"})
		if out, err := exec.CommandContext(ctx, basePython, "-m", "venv", venv).CombinedOutput(); err != nil {
			return MLX{}, fmt.Errorf("venv: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}
	emit(Event{Level: "info", Suite: "setup", Message: "installing/updating mlx and mlx-lm"})
	cmd := exec.CommandContext(ctx, py, "-m", "pip", "install", "--upgrade", "--quiet", "--disable-pip-version-check", "pip", "mlx", "mlx-lm")
	if out, err := cmd.CombinedOutput(); err != nil {
		return MLX{}, fmt.Errorf("pip install: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return MLX{Python: py, Dir: filepath.Join(dataDir, "runtime")}, nil
}
