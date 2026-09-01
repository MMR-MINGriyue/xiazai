// Package ftp implements the FTP download protocol.
//
// NOTE(M1 Task1): dependency anchor only. The blank import pins the FTP
// client dependency in go.mod until the fetcher implementation lands.
package ftp

import (
	_ "github.com/jlaffaye/ftp"
)
