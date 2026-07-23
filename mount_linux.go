package fuse

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
)

func handleFusermountStderr(errCh chan<- error) func(line string) (ignore bool) {
	return func(line string) (ignore bool) {
		if line == `fusermount: failed to open /etc/fuse.conf: Permission denied` {
			// Silence this particular message, it occurs way too
			// commonly and isn't very relevant to whether the mount
			// succeeds or not.
			return true
		}

		const (
			noMountpointPrefix = `fusermount: failed to access mountpoint `
			noMountpointSuffix = `: No such file or directory`
		)
		if strings.HasPrefix(line, noMountpointPrefix) && strings.HasSuffix(line, noMountpointSuffix) {
			// re-extract it from the error message in case some layer
			// changed the path
			mountpoint := line[len(noMountpointPrefix) : len(line)-len(noMountpointSuffix)]
			err := &MountpointDoesNotExistError{
				Path: mountpoint,
			}
			select {
			case errCh <- err:
				return true
			default:
				// not the first error; fall back to logging it
				return false
			}
		}

		return false
	}
}

// isBoringFusermountError returns whether the Wait error is
// uninteresting; exit status 1 is.
func isBoringFusermountError(err error) bool {
	if err, ok := err.(*exec.ExitError); ok && err.Exited() {
		if status, ok := err.Sys().(syscall.WaitStatus); ok && status.ExitStatus() == 1 {
			return true
		}
	}
	return false
}

func fusermountBinary() string {
	if path, err := exec.LookPath("fusermount3"); err == nil {
		return path
	}
	return "fusermount"
}

func mount(
	dir string,
	conf *mountConfig,
	ready chan<- struct{},
	errp *error,
) (fusefd *os.File, _ Backend, _ backendState, err error) {
	// linux mount is never delayed
	close(ready)

	fds, err := syscall.Socketpair(syscall.AF_FILE, syscall.SOCK_STREAM, 0)
	if err != nil {
		err = fmt.Errorf("socketpair error: %v", err)
		return
	}

	writeFile := os.NewFile(uintptr(fds[0]), "fusermount-child-writes")
	defer writeFile.Close()

	readFile := os.NewFile(uintptr(fds[1]), "fusermount-parent-reads")
	defer readFile.Close()

	cmd := exec.Command(
		fusermountBinary(),
		"-o", conf.getOptions(),
		"--",
		dir,
	)
	cmd.Env = append(os.Environ(), "_FUSE_COMMFD=3")

	cmd.ExtraFiles = []*os.File{writeFile}

	var wg sync.WaitGroup
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		err = fmt.Errorf("setting up fusermount stderr: %v", err)
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		err = fmt.Errorf("setting up fusermount stderr: %v", err)
		return
	}

	if err = cmd.Start(); err != nil {
		err = fmt.Errorf("fusermount: %v", err)
		return
	}
	helperErrCh := make(chan error, 1)
	wg.Add(2)
	go lineLogger(&wg, "mount helper output", neverIgnoreLine, stdout)
	go lineLogger(&wg, "mount helper error", handleFusermountStderr(helperErrCh), stderr)
	wg.Wait()
	if err = cmd.Wait(); err != nil {
		// see if we have a better error to report
		select {
		case helperErr := <-helperErrCh:
			// log the Wait error if it's not what we expected
			if !isBoringFusermountError(err) {
				log.Printf("mount helper failed: %v", err)
			}
			// and now return what we grabbed from stderr as the real
			// error
			err = helperErr
			return
		default:
			// nope, fall back to generic message
		}

		err = fmt.Errorf("fusermount: %v", err)
		return
	}

	c, err := net.FileConn(readFile)
	if err != nil {
		err = fmt.Errorf("FileConn from fusermount socket: %v", err)
		return
	}
	defer c.Close()

	uc, ok := c.(*net.UnixConn)
	if !ok {
		err = fmt.Errorf("unexpected FileConn type; expected UnixConn, got %T", c)
		return
	}

	buf := make([]byte, 32) // expect 1 byte
	oob := make([]byte, 32) // expect 24 bytes
	_, oobn, _, _, err := uc.ReadMsgUnix(buf, oob)
	scms, err := syscall.ParseSocketControlMessage(oob[:oobn])
	if err != nil {
		err = fmt.Errorf("ParseSocketControlMessage: %v", err)
		return
	}
	if len(scms) != 1 {
		err = fmt.Errorf("expected 1 SocketControlMessage; got scms = %#v", scms)
		return
	}
	scm := scms[0]
	gotFds, err := syscall.ParseUnixRights(&scm)
	if err != nil {
		err = fmt.Errorf("syscall.ParseUnixRights: %v", err)
		return
	}
	if len(gotFds) != 1 {
		err = fmt.Errorf("wanted 1 fd; got %#v", gotFds)
		return
	}
	fusefd = os.NewFile(uintptr(gotFds[0]), "/dev/fuse")
	return
}
