package agent

import (
	"fmt"
	"os"
	"os/exec"
)

func Run(workDir string, command []string) error {
	if len(command) == 0 {
		return fmt.Errorf("empty agent command")
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = workDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
