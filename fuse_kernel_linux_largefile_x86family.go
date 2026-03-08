//go:build linux && !arm && !arm64

package fuse

// kernelOLargeFile is the kernel ABI value of O_LARGEFILE on this
// architecture. The kernel always sets this flag on open calls before
// forwarding to FUSE, so we strip it — it's meaningless to FUSE
// filesystems. On x86/amd64 and other architectures using the generic
// asm-generic/fcntl.h, O_LARGEFILE = 0100000 octal = 0x8000.
const kernelOLargeFile uint32 = 0x8000
