# How to build MovieFinder

This guide shows how to build the app for **Windows** and for **Linux (Ubuntu)**.

Everything is built inside **Docker**, so you do **not** need to install Go or any C compiler on your computer. You only need Docker.

---

## 1. Before you build

Do these once.

### a) Install Docker

- **Windows:** install **Docker Desktop** from <https://www.docker.com/products/docker-desktop/>. Open it and wait until it says *Running*.
- **Ubuntu:** install Docker Engine:
  ```bash
  sudo apt update
  sudo apt install -y docker.io
  sudo systemctl enable --now docker
  sudo usermod -aG docker $USER    # then log out and back in
  ```

Check it works:
```bash
docker --version
```

### b) Get the code

Copy or clone the project folder onto your machine, then open a terminal **inside that folder** (the folder that contains `Dockerfile`).

### c) (Optional) Add the built-in subtitle key

The app can ship with a built-in OpenSubtitles key so subtitle search works right away. The key is **not** in the code for safety. To add it:

1. Copy `internal/opensubtitles/key.go.example` to `internal/opensubtitles/key.go`.
2. Open the new `key.go` and put your key inside the quotes.

If you skip this, the app still builds and runs — each user just types their own key in **Settings** (or leaves it blank if they don't need subtitles).

### d) (Only if a download error mentions certificates)

If your network runs an HTTPS-inspecting proxy (for example Fiddler, or a company firewall) and the build fails with `certificate signed by unknown authority`:

- On Windows, run `.\export-proxy-ca.ps1` once. It saves the needed certificate into the `certs\` folder, and the build trusts it automatically.

Most people can skip this step.

### e) (Only if a download error mentions "not available in your location")

Some networks block the default Go module server. If the build fails while downloading modules, add this to any build command:
```
--build-arg GOPROXY=https://proxy.golang.org,direct
```
(The project already defaults to a mirror that works in most places, so usually you don't need this.)

---

## 2. Build for Windows

You get a single file, `dist\MovieFinder.exe`, that runs on Windows with nothing else to install.

**Easy way (Windows, PowerShell):**
```powershell
.\build.ps1
```

**Manual way (any OS):**
```bash
docker build --target export --output "type=local,dest=dist" .
```

When it finishes, the app is at:
```
dist/MovieFinder.exe
```
Double-click it to run.

> The first build downloads the compiler tools and takes a few minutes. Later builds are much faster.

---

## 3. Build for Linux (Ubuntu)

You get a single file, `dist/MovieFinder`, that runs on Ubuntu.

**Easy way (Windows, PowerShell):**
```powershell
.\build-linux.ps1
```

**Manual way (any OS, including Ubuntu):**
```bash
docker build --target export-linux --output "type=local,dest=dist" .
```

When it finishes, the app is at:
```
dist/MovieFinder
```

To run it on Ubuntu:
```bash
chmod +x dist/MovieFinder     # once, to make it runnable
./dist/MovieFinder
```

> The app is a graphical program, so run it on a desktop Ubuntu (with a screen), not on a server with no display.

---

## 4. Where the files go

Both builds put their result in the `dist/` folder:

| Build | Command | Output |
| --- | --- | --- |
| Windows | `.\build.ps1` | `dist\MovieFinder.exe` |
| Linux | `.\build-linux.ps1` | `dist/MovieFinder` |

You can copy that one file to another computer of the same type and run it — nothing else needs to be installed.

---

## 5. Common problems

| Message | Fix |
| --- | --- |
| `Cannot connect to the Docker daemon` | Docker isn't running. Start Docker Desktop (Windows) or `sudo systemctl start docker` (Ubuntu). |
| `certificate signed by unknown authority` | See step **1d** above. |
| `this service is not available in your location` | See step **1e** above. |
| Subtitle search says "add your API key" | Either add the built-in key (step **1c**) or type your own key in the app's Settings. |

---

## 6. Running the tests (optional)

If you want to check the code before building:

```bash
docker build --target base -t moviefinder-base .
docker run --rm -v "${PWD}:/src" -w /src -e CGO_ENABLED=0 -e GOOS=linux moviefinder-base `
    go test ./internal/api/... ./internal/config/... ./internal/opensubtitles/... ./internal/delfan/... ./internal/player/... ./internal/stream/...
```

(The user-interface package is left out here because it only compiles as part of the full Windows or Linux build above.)
