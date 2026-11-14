//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package desktopusage

import "syscall"

const noFollowFlag = syscall.O_NOFOLLOW
