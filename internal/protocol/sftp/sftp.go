// Package sftp implements the SFTP download protocol.
//
// NOTE(M1 Task1): dependency anchor only. These blank imports pin the
// protocol dependencies in go.mod until the fetcher implementation lands.
package sftp

import (
	_ "github.com/gliderlabs/ssh"
	_ "github.com/pkg/sftp"
	_ "golang.org/x/crypto/ssh"
	_ "golang.org/x/net/proxy"
)
