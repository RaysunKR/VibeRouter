package protocol

import "time"

func nowNanos() int64 { return time.Now().UnixNano() }
func nowUnix() int64  { return time.Now().Unix() }
