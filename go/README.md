# Build

From this directory, with a writable build cache:

```sh
GOTOOLCHAIN=local CGO_ENABLED=0 GOPROXY=off go build -trimpath -ldflags '-s -w' -o channel .
ln -sfn channel channel-setup
ln -sfn channel channel-arm
ln -sfn channel channel-send
ln -sfn channel channel-ping
ln -sfn channel channel-status
```

If the default cache directory is not writable, set `GOCACHE` to a directory that is. The module file has no requirements. The build uses the standard library only, with cgo off, and `-ldflags '-s -w'` strips the binary. On this host that is a static ELF of about 6.5MB (net/http for the optional node webhook; it was about 2.3MB before).

`channel` prints help. `channel-setup`, `channel-arm`, and `channel-send` are the three modes. The binary is the program an agent runs.

The optional per-node webhook (`webhook=`, `webhook_key=`, `webhook_key_file=`) behaves the same as in the shell script; see [../bin/README.md](../bin/README.md#webhooks-optional).
