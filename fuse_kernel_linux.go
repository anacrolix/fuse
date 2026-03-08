package fuse

import "time"

type attr struct {
	Ino       uint64
	Size      uint64
	Blocks    uint64
	Atime     uint64
	Mtime     uint64
	Ctime     uint64
	AtimeNsec uint32
	MtimeNsec uint32
	CtimeNsec uint32
	Mode      uint32
	Nlink     uint32
	Uid       uint32
	Gid       uint32
	Rdev      uint32
	Blksize   uint32
	_         uint32
}

func (a *attr) Crtime() time.Time {
	return time.Time{}
}

func (a *attr) SetCrtime(s uint64, ns uint32) {
	// Ignored on Linux.
}

func (a *attr) SetFlags(f uint32) {
	// Ignored on Linux.
}

type setattrIn struct {
	setattrInCommon
}

func (in *setattrIn) BkupTime() time.Time {
	return time.Time{}
}

func (in *setattrIn) Chgtime() time.Time {
	return time.Time{}
}

func (in *setattrIn) Flags() uint32 {
	return 0
}

func openFlags(flags uint32) OpenFlags {
	// The kernel always sets O_LARGEFILE on open calls before forwarding to
	// FUSE. Its value varies by architecture (see kernelOLargeFile), and it's
	// meaningless to FUSE filesystems, so we strip it.
	flags &^= kernelOLargeFile

	return OpenFlags(flags)
}

type getxattrIn struct {
	getxattrInCommon
}

type setxattrIn struct {
	setxattrInCommon
}
