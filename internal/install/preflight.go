package install

import (
	"fmt"
	"os/exec"
	"strings"
)

// CheckDependencies runs before any installation step changes the server.
// ufw is optional; ConfigureFirewall reports missing ufw as a skipped step.
func CheckDependencies() error {
	required := []string{"bash", "curl", "openssl", "chmod", "systemctl", "journalctl"}
	var missing []string
	for _, command := range required {
		if _, err := exec.LookPath(command); err != nil {
			missing = append(missing, command)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required dependencies: %s; install them and run hy2tool install again (no installation steps have run)", strings.Join(missing, ", "))
	}
	return nil
}
