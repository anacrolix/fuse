package fuse

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"unsafe"
)

const FUSET_SRV_PATH = "/usr/local/bin/go-nfsv4"

func fusetBinary() (string, error) {
	srv_path := os.Getenv("FUSE_NFSSRV_PATH")
	if srv_path == "" {
		srv_path = FUSET_SRV_PATH
	}

	if _, err := os.Stat(srv_path); err == nil {
		return srv_path, nil
	}

	return "", fmt.Errorf("FUSE-T not found")
}

func mountFuseT(
	bin string,
	mountPoint string,
	conf *mountConfig,
	ready chan<- struct{},
	errp *error,
) (_ *os.File, _ fuseTBackendState, err error) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return
	}
	local := fds[0]
	remote := fds[1]

	defer syscall.Close(remote)

	fds, err = syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return
	}

	local_mon := fds[0]
	remote_mon := fds[1]

	defer syscall.Close(remote_mon)

	args := []string{
	}
	if conf.isReadonly() {
		args = append(args, "-r")
	}
	if conf.fsname() != "" {
		args = append(args, "--volname="+conf.fsname())
	}
	// TODO: apply more args

	remote_file := os.NewFile(uintptr(remote), "")
	remote_mon_file := os.NewFile(uintptr(remote_mon), "")
	local_file := os.NewFile(uintptr(local), "")
	local_mon_file := os.NewFile(uintptr(local_mon), "")

	args = append(args, fmt.Sprintf("--rwsize=%d", maxWrite))
	args = append(args, mountPoint)
	cmd := exec.Command(bin, args...)
	cmd.ExtraFiles = []*os.File{remote_file, remote_mon_file} // fd would be (index + 3)
	cmd.Stderr = nil
	cmd.Stdout = nil
	// daemonize
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	envs := []string{}
	envs = append(envs, "_FUSE_COMMFD=3")
	envs = append(envs, "_FUSE_MONFD=4")
	envs = append(envs, "_FUSE_COMMVERS=2")
	cmd.Env = append(os.Environ(), envs...)

	syscall.CloseOnExec(local)
	syscall.CloseOnExec(local_mon)

	if err = cmd.Start(); err != nil {
		return
	}

	cmd.Process.Release()
	go func() {
		var err error
		if _, err = local_mon_file.Write([]byte("mount")); err != nil {
			err = fmt.Errorf("fuse-t failed: %v", err)
		} else {
			reply := make([]byte, 4)
			if _, err = local_mon_file.Read(reply); err != nil {
				err = fmt.Errorf("fuse-t failed: %v", err)
			}
			if !bytes.Equal(reply, []byte{0x0, 0x0, 0x0, 0x0}) {
				err = fmt.Errorf("moint failed")
			}
		}

		*errp = err
		close(ready)
	}()

	return local_file, fuseTBackendState{
		// Prevent these files from being GCed.
		extraFiles: []*os.File{
			local_mon_file,
		},
	}, err
}

func mount(
	mountPoint string,
	conf *mountConfig,
	ready chan<- struct{},
	errp *error,
) (
	f *os.File,
	be Backend,
	bes backendState,
	err error,
) {
	if forcedBackend.IsUnset() || forcedBackend.IsFuseT() {
		var fuseTBin string
		fuseTBin, err = fusetBinary()
		if err == nil {
			f, bes, err = mountFuseT(fuseTBin, mountPoint, conf, ready, errp)
			be = fuseTBackend
			return
		}
		if forcedBackend.IsFuseT() {
			return
		}
	}

	locations := conf.osxfuseLocations
	if locations == nil {
		locations = []OSXFUSEPaths{
			OSXFUSELocationV4,
			OSXFUSELocationV3,
		}
	}

	var binLocation string
	for _, loc := range locations {
		if _, err := os.Stat(loc.Mount); os.IsNotExist(err) {
			// try the other locations
			continue
		}
		binLocation = loc.Mount
		break
	}
	if binLocation == "" {
		err = ErrOSXFUSENotFound
		return
	}

	local, remote, err := unixgramSocketpair()
	if err != nil {
		return
	}

	defer local.Close()
	defer remote.Close()

	cmd := exec.Command(binLocation,
		"-o", conf.getOptions(),

		// Tell osxfuse-kext how large our buffer is. It must split
		// writes larger than this into multiple writes.
		//
		// OSXFUSE seems to ignore InitResponse.MaxWrite, and uses
		// this instead.
		"-o", "iosize="+strconv.FormatUint(maxWrite, 10),

		mountPoint)

	cmd.ExtraFiles = []*os.File{remote} // fd would be (index + 3)
	cmd.Env = append(os.Environ(),
		"_FUSE_CALL_BY_LIB=",
		"_FUSE_DAEMON_PATH="+os.Args[0],
		"_FUSE_COMMFD=3",
		"_FUSE_COMMVERS=2",
		"MOUNT_OSXFUSE_CALL_BY_LIB=",
		"MOUNT_OSXFUSE_DAEMON_PATH="+os.Args[0])

	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	if err = cmd.Start(); err != nil {
		return
	}

	fd, err := getConnection(local)
	if err != nil {
		return
	}

	go func() {
		// wait inside a goroutine, or otherwise it would block forever for unknown reasons
		if err := cmd.Wait(); err != nil {
			err = fmt.Errorf("mount_osxfusefs failed: %v. Stderr: %s, Stdout: %s",
				err, errOut.String(), out.String())
			*errp = err
		}
		close(ready)
	}()

	dup, err := syscall.Dup(int(fd.Fd()))
	if err != nil {
		return
	}

	syscall.CloseOnExec(int(fd.Fd()))
	syscall.CloseOnExec(dup)

	f = os.NewFile(uintptr(dup), "macfuse")
	be = osxfuseBackend
	bes = nopBackendState{}
	return
}

func unixgramSocketpair() (l, r *os.File, err error) {
	fd, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, nil, os.NewSyscallError("socketpair",
			err.(syscall.Errno))
	}
	l = os.NewFile(uintptr(fd[0]), "socketpair-half1")
	r = os.NewFile(uintptr(fd[1]), "socketpair-half2")
	return
}

func getConnection(local *os.File) (*os.File, error) {
	var data [4]byte
	control := make([]byte, 4*256)

	// n, oobn, recvflags, from, errno  - todo: error checking.
	_, oobn, _, _,
		err := syscall.Recvmsg(
		int(local.Fd()), data[:], control[:], 0)
	if err != nil {
		return nil, err
	}

	message := *(*syscall.Cmsghdr)(unsafe.Pointer(&control[0]))
	fd := *(*int32)(unsafe.Pointer(uintptr(unsafe.Pointer(&control[0])) + syscall.SizeofCmsghdr))

	if message.Type != syscall.SCM_RIGHTS {
		return nil, fmt.Errorf("getConnection: recvmsg returned wrong control type: %d", message.Type)
	}
	if oobn <= syscall.SizeofCmsghdr {
		return nil, fmt.Errorf("getConnection: too short control message. Length: %d", oobn)
	}
	if fd < 0 {
		return nil, fmt.Errorf("getConnection: fd < 0: %d", fd)
	}

	return os.NewFile(uintptr(fd), "macfuse"), nil
}
