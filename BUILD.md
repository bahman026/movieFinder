# How to build MovieFinder

This guide shows how to build the app for **Windows**, **Linux (Ubuntu)** and **macOS**.

The Windows and Linux builds run inside **Docker**, so for those you do **not** need to install Go or any C compiler. You only need Docker.

**macOS is the exception.** A Mac app must be built on a Mac, with Go installed — Docker cannot produce one. The graphics toolkit draws through Apple's Cocoa frameworks, and those come only from the macOS SDK on a real Mac, so no Linux container can compile them. See [section 4](#4-build-for-macos).

---

## 1. Before you build

Do these once.

### a) Install Docker — *Windows and Linux only*

Building **only** for macOS? Skip this and do step **1f** instead.

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

### f) (macOS only) Install Go and Apple's build tools

The Mac build compiles on your machine instead of in a container, so it needs two things:

```bash
xcode-select --install     # Apple's compiler and the macOS SDK (skip if already installed)
brew install go            # the Go toolchain
```

If you don't have Homebrew, get it from <https://brew.sh>, or install Go from <https://go.dev/dl/>.

Check both are ready:
```bash
go version
xcode-select -p
```

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

## 4. Build for macOS

You get **`dist/MovieFinder.app`** — a normal Mac app with its own icon, which you can drag into your Applications folder.

> **This build does not use Docker.** It runs on your Mac directly, so make sure you did step **1f** first.

**Easy way:**
```bash
./build-mac.sh
```

**Manual way** (the same two steps the script runs):
```bash
# 1. Compile the program
CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 \
    go build -trimpath -ldflags "-s -w" -o dist/MovieFinder ./cmd/moviefinder

# 2. Wrap it in a .app bundle, so it gets its icon and its name in the Dock
go run fyne.io/tools/cmd/fyne@latest package \
    -os darwin --exe dist/MovieFinder --icon Icon.png \
    --name MovieFinder --app-id com.moviefinder.app
mv MovieFinder.app dist/
```

To run it:
```bash
open dist/MovieFinder.app
```

### Which chip to build for

- **Apple silicon** (M1 and newer) — `arm64`. This is the default.
- **Intel Mac** — `amd64`.

```bash
./build-mac.sh amd64          # or set GOARCH=amd64 in the manual command
```

A Mac app built for `arm64` will not run on an Intel Mac. The other direction works, because Apple silicon can run Intel apps through Rosetta.

### Careful: the packaging step edits a project file

`fyne package` **rewrites `FyneApp.toml`** while it runs — it bumps the `Build` number and removes empty fields. `build-mac.sh` restores the file for you. If you run the manual command instead, put it back yourself so the change doesn't sneak into a commit:

```bash
git checkout -- FyneApp.toml
```

### Sending the app to someone else

**A `.app` is a folder, not a file.** Telegram, WhatsApp and email have nothing to attach when you point them at it, no matter how small it is. Compress it into one file first.

`./build-mac.sh` already writes that file for you:

```
dist/MovieFinder-mac-arm64.zip     (about 11 MB)
```

Send **that**. To make it by hand instead, either right-click `MovieFinder.app` in Finder → **Compress**, or:

```bash
cd dist && ditto -c -k --sequesterRsrc --keepParent MovieFinder.app MovieFinder-mac.zip
```

> Use `ditto`, not `zip`. A Mac app bundle contains symlinks and file metadata that plain `zip` can flatten, which produces an app that will not start on the other end.

Two things to tell whoever receives it:

1. **Unzip it first, then move `MovieFinder.app` to Applications.** Running it from inside the Downloads zip preview does not work.
2. **The first launch needs approval** — see just below.

Also check which Mac they have: the `arm64` file will not run on an Intel Mac. Ask them, or send both (`./build-mac.sh amd64` builds the Intel one).

### Opening the app on a different Mac

The app is not signed with an Apple developer certificate. On the Mac that built it, it just opens. Copy it to **another** Mac and macOS will quarantine it and refuse to start it (*"Apple could not verify..."*). On that Mac, either:

**On macOS 15 (Sequoia) and newer** — including macOS 26 — do this once, on the receiving Mac:

1. Double-click the app. macOS refuses and says it cannot verify the developer.
2. Open **System Settings → Privacy & Security**, scroll to the bottom.
3. There is now a line about MovieFinder being blocked — click **Open Anyway**.
4. Confirm. From then on it opens normally.

> The old trick of right-clicking the app and choosing **Open** no longer works on these versions; Apple removed that shortcut in macOS 15. The Settings route above is the replacement.

If they are comfortable in Terminal, this does the same thing in one step:
```bash
xattr -dr com.apple.quarantine /Applications/MovieFinder.app
```

> **Why this happens:** signing an app so it opens with no warning requires a paid Apple Developer account ($99/year) and notarising each build with Apple. This app is only ad-hoc signed, which is enough for macOS to run it, but not enough for it to be trusted silently.

### Playing videos needs a separate player

MovieFinder has no built-in video decoder — it hands the stream to a player already on your Mac. Install one:

```bash
brew install --cask vlc       # or:  brew install mpv       or:  brew install --cask iina
```

MovieFinder looks for VLC, mpv and IINA in their usual install locations. If yours is somewhere unusual, type the full path in **Settings → Video player**.

> Why full paths and not just "whatever is on my PATH"? An app started from Finder does not inherit the `PATH` from your terminal, so a player that works when you type its name in a shell can still be invisible to the app. That's why the standard locations are checked directly.

---

## 5. Where the files go

Every build puts its result in the `dist/` folder:

| Build | Command | Output | Built with |
| --- | --- | --- | --- |
| Windows | `.\build.ps1` | `dist\MovieFinder.exe` | Docker |
| Linux | `.\build-linux.ps1` | `dist/MovieFinder` | Docker |
| macOS | `./build-mac.sh` | `dist/MovieFinder.app` **+** `dist/MovieFinder-mac-arm64.zip` | Go on the Mac itself |

You can copy that one file (or, on macOS, the one `.app` folder) to another computer of the same type and run it — nothing else needs to be installed.

---

## 6. Common problems

| Message | Fix |
| --- | --- |
| `Cannot connect to the Docker daemon` | Docker isn't running. Start Docker Desktop (Windows) or `sudo systemctl start docker` (Ubuntu). |
| `certificate signed by unknown authority` | See step **1d** above. |
| `this service is not available in your location` | See step **1e** above. |
| Subtitle search says "add your API key" | Either add the built-in key (step **1c**) or type your own key in the app's Settings. |
| macOS: `go: command not found` | Go isn't installed — see step **1f**. |
| macOS: build stops on a missing header or SDK | Apple's tools are missing: `xcode-select --install` (step **1f**). |
| macOS: *"Apple could not verify..."* when opening | Expected for an unsigned app copied from another Mac — see [section 4](#opening-the-app-on-a-different-mac). |
| macOS: *"MovieFinder is damaged and can't be opened"* | The bundle's signature is broken — rebuild with `./build-mac.sh`, which signs it, and send the `.zip` it produces rather than a hand-made one. |
| Can't attach the app to Telegram / email | A `.app` is a folder. Send `dist/MovieFinder-mac-arm64.zip` instead — see [section 4](#sending-the-app-to-someone-else). |
| macOS: `FyneApp.toml` shows up as changed in git | The packaging step rewrote it. `git checkout -- FyneApp.toml`, and prefer `./build-mac.sh`, which restores it automatically. |
| "No video player found" | Install VLC, mpv or IINA, or set the player path in **Settings** — see the end of [section 4](#4-build-for-macos). |

---

## 7. Running the tests (optional)

If you want to check the code before building:

```bash
docker build --target base -t moviefinder-base .
docker run --rm -v "${PWD}:/src" -w /src -e CGO_ENABLED=0 -e GOOS=linux moviefinder-base `
    go test ./internal/api/... ./internal/config/... ./internal/opensubtitles/... ./internal/delfan/... ./internal/player/... ./internal/stream/...
```

**On a Mac**, once step **1f** is done you can run them directly — no Docker needed:

```bash
go test ./internal/api/... ./internal/config/... ./internal/opensubtitles/... ./internal/delfan/... ./internal/player/... ./internal/stream/...
```

(The user-interface package is left out here because its tests need a real screen. It still gets compiled as part of any of the three builds above, which is what catches mistakes in it.)
