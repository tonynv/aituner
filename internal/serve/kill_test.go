package serve

import "syscall"

// syscallKill0 probes whether a process exists (signal 0 delivers nothing).
func syscallKill0(pid int) error { return syscall.Kill(pid, 0) }
