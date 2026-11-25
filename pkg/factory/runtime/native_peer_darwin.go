package runtime

import "golang.org/x/sys/unix"

// nativePeerUID reports the effective UID of the connected Unix socket peer.
func nativePeerUID(fd int) (uint32, bool) {
	identity, err := unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil {
		return 0, false
	}
	return identity.Uid, true
}
