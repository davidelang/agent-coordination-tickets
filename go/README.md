# Build

From this directory, with a writable build cache:

```sh
GOTOOLCHAIN=local CGO_ENABLED=0 GOPROXY=off go build -trimpath -ldflags '-s -w' -o channel .
ln -sfn channel channel-setup
ln -sfn channel channel-arm
ln -sfn channel channel-send
```

If the default cache directory is not writable, set `GOCACHE` to a directory that is. The module file has no requirements. The build uses the standard library only, with cgo off, and `-ldflags '-s -w'` strips the binary. On this host that is a static ELF of about 2.3MB.

`channel` prints help. `channel-setup`, `channel-arm`, and `channel-send` are the three modes. The binary is the program an agent runs.
