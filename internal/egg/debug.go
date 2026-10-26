package egg

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func (e *Egg) command(p Prepared) (*exec.Cmd, error) {
	args := p.Args
	if len(args) == 0 {
		return nil, errors.New("empty launch command")
	}
	if e.Config.GDBDebugPort == 0 && strings.TrimSpace(e.Config.CustomParams) == "" {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = e.Config.Dir
		return cmd, nil
	}
	if port := e.Config.GDBDebugPort; port < 0 || port > 65535 || port == e.Config.Port {
		return nil, errors.New("invalid debug port")
	}
	debugger := ""
	if e.Config.GDBDebugPort != 0 {
		var err error
		debugger, err = exec.LookPath("gdbserver")
		if err != nil {
			return nil, errors.New("GDB_DEBUG_PORT requires gdbserver in the runtime image")
		}
	}
	root, err := os.OpenRoot(e.Config.Dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	candidates := []string{"srcds_linux64", "srcds_linux"}
	if p.Profile.Runtime == "linux32" {
		candidates = []string{"srcds_linux"}
	}
	if p.Profile.Runtime == "linux64" {
		candidates = []string{"srcds_linux64"}
	}
	binary := ""
	for _, name := range candidates {
		st, err := root.Stat(name)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !st.Mode().IsRegular() || st.Mode().Perm()&0111 == 0 {
			return nil, fmt.Errorf("engine target %s is not executable", name)
		}
		binary = "./" + name
		break
	}
	if binary == "" {
		return nil, errors.New("no Source engine binary available for direct argv launch")
	}
	cmd := exec.Command(binary, args[1:]...)
	if debugger != "" {
		debugArgs := []string{"--once", "0.0.0.0:" + strconv.Itoa(e.Config.GDBDebugPort), binary}
		cmd = exec.Command(debugger, append(debugArgs, args[1:]...)...)
	}
	cmd.Dir = e.Config.Dir
	// Direct execution preserves quoted custom argv across Valve wrappers and
	// gives gdbserver an ELF inferior. Supply the wrapper's library search paths.
	libs := []string{e.Config.Dir}
	if strings.HasSuffix(binary, "64") {
		libs = append(libs, filepath.Join(e.Config.Dir, "bin/linux64"), filepath.Join(e.Config.Dir, p.Profile.Dir, "bin/linux64"))
	}
	libs = append(libs, filepath.Join(e.Config.Dir, "bin"), filepath.Join(e.Config.Dir, p.Profile.Dir, "bin"))
	if previous := os.Getenv("LD_LIBRARY_PATH"); previous != "" {
		libs = append(libs, previous)
	}
	cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH="+strings.Join(libs, ":"))
	return cmd, nil
}
