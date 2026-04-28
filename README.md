# Simple File Store Desktop

A Wails desktop uploader for Simple File Store. It uses the server's resumable upload API from Go instead of the browser/webview, allowing higher parallel chunk counts and avoiding browser connection limits.

## Features

- Login before selecting files or uploading.
- Stores the JWT only in memory for the current app session.
- Uploads through Go's HTTP client with resumable chunks.
- Retries each failed chunk up to 5 times.
- Stops immediately when the server returns `401 Unauthorized` and asks the user to login again.
- Uses plain HTML, CSS, and JavaScript for the frontend. No frontend framework or build tool is required.

## Requirements

- Go 1.26 or newer.
- Wails system dependencies for your platform.
- Optional, for regenerating the app icon from the server SVG: `librsvg` with `rsvg-convert`.

The Wails CLI is installed as a project-local Go tool. Do not install it globally for this app.

## Setup

From this directory:

```sh
go mod tidy
```

If the local Wails tool needs to be restored manually:

```sh
go get -tool github.com/wailsapp/wails/v2/cmd/wails@latest
```

## Development

Run the app in development mode:

```sh
go tool wails dev
```

Build the desktop app:

```sh
go tool wails build
```

## Regenerate Icon

The desktop icon is based on `<path-to-simple-file-store>/src/assets/favicon.svg`.

```sh
rsvg-convert -w 1024 -h 1024 <path-to-simple-file-store>/src/assets/favicon.svg -o build/appicon.png
go tool wails build
```

## Usage

1. Start a Simple File Store server.
2. Open the desktop app.
3. Enter the server URL, username, and password.
4. Login first. The app stores the JWT in memory only.
5. Select a file and configure the remote folder and parallel chunk count.
6. Start the upload.

If the server returns `401 Unauthorized`, the upload stops, the in-memory session is cleared, and the app asks you to login again.

## Notes

- The upload logic intentionally lives in Go, not JavaScript.
- The app uses the existing server endpoints: `/login` and `/upload/<path>`.
- Generated build output under `build/bin` is ignored by git.
