# Simple File Store Desktop

Desktop uploader application built with Go + Wails, for the [simple-file-store](https://github.com/hronro/simple-file-store) service.

## Features

- Configurable service endpoint
- Configurable upload thread count
- Login with username/password using server `/login`
- JWT expiry check before authenticated requests
- True multi-connection chunk upload in Go (HTTP/2 disabled for upload clients)
- Upload progress events per file and per chunk

## How it works

1. Login request is sent to `POST /login`.
2. The app extracts `access_token` from response cookies.
3. Before each authenticated call, token `exp` is checked locally.
4. Upload uses server resumable endpoints:
   - `GET /upload/{file}`
   - `POST /upload/{file}`
   - `PUT /upload/{file}`
5. Chunk workers run in parallel, each with its own HTTP transport and HTTP/2 disabled.

## Run in dev mode

```bash
go tool wails dev
```

## Build

```bash
go tool wails build
```

## Notes

- The app currently focuses on upload workflow only.
- It does not implement file listing or download UI.
