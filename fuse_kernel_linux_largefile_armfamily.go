//go:build linux && (arm || arm64)

package fuse

// kernelOLargeFile is the kernel ABI value of O_LARGEFILE on this
// architecture. The kernel always sets this flag on open calls before
// forwarding to FUSE, so we strip it — it's meaningless to FUSE
// filesystems. On arm/arm64, arch/arm64/include/uapi/asm/fcntl.h
// defines O_LARGEFILE = 0400000 octal = 0x20000.
// (Note: on arm64, 0x8000 = O_NOFOLLOW, not O_LARGEFILE.)
const kernelOLargeFile uint32 = 0x20000
