# go-ftp

[![Go Reference](https://pkg.go.dev/badge/github.com/natalie-o-perret/go-ftp.svg)](https://pkg.go.dev/github.com/natalie-o-perret/go-ftp)

A focused, embeddable FTP + FTPS library and server in pure Go.

> RFC 959 control channel, RFC 4217 explicit FTPS, implicit FTPS,
> RFC 2389 FEAT, RFC 2428 EPSV/EPRT, RFC 2640 UTF-8, RFC 3659 SIZE/MDTM/MLSx.
> No CGo. No reflection. Cleanly composable.

## Packages

| Package | Import path | Purpose |
| --- | --- | --- |
| `ftp` | `.../go-ftp/ftp` | Wire protocol: reply codes, command parsing |
| `client` | `.../go-ftp/client` | FTP control-channel client |
| `client/ftps` | `.../go-ftp/client/ftps` | Explicit and implicit FTPS client |
| `server` | `.../go-ftp/server` | FTP server |
| `server/auth` | `.../go-ftp/server/auth` | Authenticator backends |
| `server/fs` | `.../go-ftp/server/fs` | Filesystem backends |
| `config` | `.../go-ftp/config` | TOML config loader |
| `internal/*` | `.../go-ftp/internal/*` | Shared internals |

## Quick start

### Library

```go
import (
    "github.com/natalie-o-perret/go-ftp/client"
    "github.com/natalie-o-perret/go-ftp/client/ftps"
)

c, err := client.New(client.Config{
    Addr: "ftp.example.com:21",
    User: "alice",
    Password: "hunter2",
})
if err != nil { log.Fatal(err) }
defer c.Close()

if err := c.Login("alice", "hunter2", ""); err != nil { log.Fatal(err) }

if err := c.Type("I"); err != nil { log.Fatal(err) }
rc, done, err := c.Retr("pub/readme.txt")
if err != nil { log.Fatal(err) }
io.Copy(os.Stdout, rc)
rc.Close()
done()
```

Explicit FTPS:

```go
c, err := ftps.Connect(ftps.Config{
    Config: client.Config{Addr: "ftp.example.com:21", User: "alice", Password: "hunter2"},
    TLSConfig: ftps.DefaultTLSConfig("ftp.example.com"),
    Explicit: true,
    ProtectDataChannel: true,
})
```

### Server

```go
import (
    "github.com/natalie-o-perret/go-ftp/server"
    "github.com/natalie-o-perret/go-ftp/server/auth"
    "github.com/natalie-o-perret/go-ftp/server/fs"
)

srv, _ := server.New(server.Config{
    Name: "my-ftp",
    Listen: ":21",
    Authenticator: auth.NewStatic(),
    Filesystem: fs.NewOS("/srv/ftp"),
})
log.Fatal(srv.ListenAndServe())
```

## CLI

```sh
go build ./cmd/ftpd
./ftpd -config config/ftpd.example.toml
```

## Licence

MIT.
