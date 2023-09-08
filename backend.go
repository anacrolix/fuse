package fuse

import (
	"github.com/anacrolix/log"
	"os"
	"strings"
)

type Backend string

const (
	fuseTBackend   = "FUSE-T"
	osxfuseBackend = "OSXFUSE"
)

func (be Backend) IsFuseT() bool {
	return be == fuseTBackend
}

func (be Backend) IsUnset() bool {
	return be == ""
}

var forcedBackend Backend

func initForcedBackend() {
	forcedBackend = getForcedBackend()
}

func getForcedBackend() (ret Backend) {
	ret = Backend(strings.ToUpper(strings.TrimSpace(os.Getenv("FUSE_FORCE_BACKEND"))))
	if !ret.IsUnset() {
		Logger.Levelf(log.Info, "forcing backend %v", ret)
	}
	return
}

// Extra state to be managed per backend.
type backendState interface {
	Drop()
}

// FUSE-T requires we hold on to some extra file descriptors for the duration of the connection.
type fuseTBackendState struct {
	extraFiles []*os.File
}

func (bes fuseTBackendState) Drop() {
	for _, f := range bes.extraFiles {
		f.Close()
	}
}

type nopBackendState struct{}

func (nopBackendState) Drop() {}
