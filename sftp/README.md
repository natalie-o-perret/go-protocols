# go-sftp

[![Go Reference](https://pkg.go.dev/badge/github.com/natalie-o-perret/go-sftp.svg)](https://pkg.go.dev/github.com/natalie-o-perret/go-sftp)

A focused, embeddable SFTP library and server in Go.

> SSH File Transfer Protocol (draft-ietf-secsh-filexfer-13) client
> and server, running on top of `golang.org/x/crypto/ssh`. Pure
> Go. No CGo.

## Packages

| Package | Import path | Purpose |
| --- | --- | --- |
| `sftp` | `.../go-sftp/sftp` | Wire protocol: packet types, length-prefixed codecs |
| `client` | `.../go-sftp/client` | High-level SFTP client |
| `server` | `.../go-sftp/server` | SFTP server subsystem handler |
| `server/backend/osfs` | `.../go-sftp/server/backend/osfs` | OS-backed filesystem backend |

## Quick start

### Library client

The client takes an established SSH channel that has been
requested as the `sftp` subsystem:

```go
import (
    "github.com/natalie-o-perret/go-sftp/client"
    "golang.org/x/crypto/ssh"
)

cfg := &ssh.ClientConfig{ User: "alice", Auth: []ssh.AuthMethod{ssh.Password("hunter2")} }
conn, _ := ssh.Dial("tcp", "example.com:22", cfg)
defer conn.Close()

ch, _, _ := conn.OpenChannel("session", nil)
reqs, _ := ch.SendRequest("subsystem", false, ssh.Marshal(struct{ Name string }{Name: "sftp"}))
ssh.DiscardRequests(reqs)

c, err := client.New(ch)
defer c.Close()

entries, _ := c.ReadDir(ctx, "/")
for _, e := range entries {
    fmt.Println(e.Name, e.Attr.Size)
}
```

### Embedding the server

```go
import (
    "github.com/natalie-o-perret/go-sftp/server"
    "github.com/natalie-o-perret/go-sftp/server/backend/osfs"
)

srv := server.New(osfs.New("/srv/files"))
err := srv.Serve(ch)
```

## CLI

```sh
ssh-keygen -t ed25519 -f hostkey
go build ./cmd/sftpd
./sftpd -addr :2022 -root /srv/files -hostkey hostkey
```

## License

MIT.
