# wget-v2 Audit

## Scope

This audit is based on:

- `subject.txt` requirements
- `audit.txt` evaluation checklist
- Static review of the Go source files
- One successful `go build ./...`

No code was edited for this audit. Network-based functional checks were not completed in this environment, so runtime statements below are based on source analysis unless explicitly stated otherwise.

## Overall Assessment

The project compiles, but it is not yet aligned with the subject requirements. It implements the basic CLI shape and some core ideas:

- single HTTP download
- single FTP download
- output path and output name flags
- rate-limit parsing and throttled copying
- input file reading with `-i`
- mirror mode using `colly`
- reject/exclude/convert-links flags in mirror mode

However, several required behaviors are either missing or implemented incorrectly. The most important gaps are:

- HTTP response handling is wrong for non-200 responses
- downloads from `-i` are sequential, not asynchronous
- background mode is not actually background mode
- required start/finish/reporting format is incomplete
- mirroring is only partial and does not satisfy the subject reliably

## What The Project Currently Handles

### Implemented

- Parses these flags in [flags.go](flags.go):
  - `-O`
  - `-P`
  - `--rate-limit`
  - `-i`
  - `-B`
  - `--mirror`
  - `--convert-links`
  - `-R`
  - `-X`
- Routes `ftp://...` URLs to FTP handling in [main.go:22](main.go#L22)
- Performs basic HTTP GET in [http.go](http.go)
- Saves downloaded content to disk in [io.go](io.go)
- Creates a mirrored folder named from the URL host in [mirror.go](mirror.go)

### Partially Implemented

- Progress display exists through `progressbar`, but the exact required output format is not controlled by the project
- `--rate-limit` exists, but validation and failure handling are weak
- `--convert-links`, `-R`, and `-X` exist, but their behavior is much more limited than the subject expects

## High-Severity Findings

### 1. HTTP downloads proceed even when the server does not return `200 OK`

Location:

- [http.go:18-22](http.go#L18)

Problem:

- The code prints `status %d OK` for every response code.
- It never checks `resp.StatusCode == http.StatusOK`.
- It will download error pages such as `404 Not Found` or `500 Internal Server Error` as if the request succeeded.

Impact:

- Direct failure against the subject, which requires the program to stop unless the response is `200 OK`.
- Audit checks expecting exact status handling will fail.

Expected behavior:

- Print the actual response status, for example `404 Not Found`.
- Abort the download when the status is not `200 OK`.

### 2. `-i` downloads are not asynchronous

Location:

- [main.go:19-24](main.go#L19)

Problem:

- URLs are processed in a simple `for` loop.
- There are no goroutines, no worker pool, and no synchronization.

Impact:

- The requirement for asynchronous multi-download is not met.
- The audit checklist explicitly checks that downloads from the input file happen at the same time.

Expected behavior:

- Multiple downloads from `-i` should run concurrently and report completion independently.

### 3. Background mode is not actually running in the background

Location:

- [main.go:12-16](main.go#L12)

Problem:

- `-B` only redirects `stdout` and `stderr` to `wget-log`.
- The same process continues running in the foreground.
- Nothing is detached, forked, or re-executed independently.

Impact:

- This does not satisfy the subject requirement for downloading in background.
- The shell remains tied to the running process.

Expected behavior:

- The command should return immediately after spawning detached work, with output written to `wget-log`.

### 4. Required download report is incomplete

Locations:

- [http.go:10-22](http.go)
- [io.go:27-41](io.go)
- [ftp.go:12-35](ftp.go)

Problems:

- No `finished at yyyy-mm-dd hh:mm:ss` output exists after downloads complete.
- HTTP prints only start time, status, size, save path, and final downloaded URL.
- FTP does not follow the required reporting format at all.

Impact:

- The project will fail checklist items that require the finish timestamp and the expected log structure.
- `wget-log` output in background mode cannot match the expected audit format.

### 5. Mirror mode does not fully implement website mirroring as described

Location:

- [mirror.go](mirror.go)

Problems:

- The crawler only follows links found in HTML selectors `a`, `link`, `img`, and `script`.
- The subject explicitly says HTML or CSS should be retrieved and parsed; CSS content is not parsed for nested resources such as `url(...)`.
- `--convert-links` is implemented as a raw string replacement of the base URL with `"./"`, which is not a correct offline link conversion strategy.
- Query-string variants are not preserved in the output path, which can cause collisions.
- Reject/exclude checks are simplistic and may miss real cases.

Impact:

- Some mirrored sites will be incomplete or broken offline even when `--convert-links` is supplied.
- The feature may appear to work on simple sites but fail on realistic websites.

## Medium-Severity Findings

### 6. Status text is fabricated instead of using the real HTTP status

Location:

- [http.go:18](http.go#L18)

Problem:

- The code prints `status %d OK` instead of `resp.Status`.

Impact:

- A `301`, `404`, or `500` would still print as `OK`.
- This is misleading and will fail exact-output audits.

### 7. Background output message does not match the subject

Location:

- [main.go:13](main.go#L13)

Problem:

- The project prints `Output written to wget-log`
- The subject and audit expect `Output will be written to "wget-log".`

Impact:

- Likely output mismatch in evaluation.

### 8. Background logs will include progress-bar output

Locations:

- [main.go:12-16](main.go#L12)
- [io.go:31-40](io.go#L31)

Problem:

- In background mode the program redirects normal stdout to `wget-log`.
- The progress bar also writes into that redirected stdout.
- The expected log structure in the subject does not include a live progress bar.

Impact:

- `wget-log` content will not match the expected silent/background log format.

### 9. `~/Downloads` style paths are not expanded

Location:

- [io.go:24-25](io.go#L24)

Problem:

- `filepath.Join(outP, name)` uses the literal `outP`.
- If the user passes `-P=~/Downloads/`, the program will create a local directory literally named `~` instead of using the home directory.

Impact:

- Direct mismatch with the subject example for `-P=~/Downloads/`.

### 10. Errors from file and directory operations are ignored

Locations:

- [main.go:14](main.go#L14)
- [main.go:30](main.go#L30)
- [io.go:25](io.go#L25)
- [io.go:28](io.go#L28)
- [io.go:40](io.go#L40)
- [mirror.go:50](mirror.go#L50)
- [mirror.go:57](mirror.go#L57)

Problem:

- The code ignores many errors from `os.Create`, `os.Open`, `os.MkdirAll`, `io.Copy`, and `os.WriteFile`.

Impact:

- The program can claim success while silently failing to save the file.
- Missing permissions, missing directories, and short writes would be hard to diagnose.

### 11. The input file reader leaks the file handle and ignores scanner errors

Location:

- [main.go:30-36](main.go#L30)

Problem:

- The file opened with `os.Open(fileI)` is never closed.
- `Scanner.Err()` is never checked.

Impact:

- Resource leak and silent input-file failures.

### 12. FTP mode does not behave like the required wget flow

Location:

- [ftp.go](ftp.go)

Problem:

- FTP downloads print `Downloading FTP: ...` instead of the required audit/report lines.
- Login errors are ignored.
- The size is unknown and passed as `-1`, so progress reporting cannot match the HTTP-style requirements.

Impact:

- FTP exists, but its UX and reporting are inconsistent with the rest of the project.

### 13. Invalid or malformed rate limits are accepted silently

Locations:

- [io.go:34-37](io.go#L34)
- [io.go:44-56](io.go#L44)

Problem:

- `parseRate` ignores parsing failures and returns `0` on invalid input.
- The limiter is then created with `0`, and errors from `WaitN` are ignored.

Impact:

- Invalid input such as `--rate-limit=abc` is not rejected clearly.
- Throttling behavior becomes undefined or ineffective.

### 14. Exit codes are not meaningful

Locations:

- [http.go:12-14](http.go#L12)
- [ftp.go:21-31](ftp.go#L21)

Problem:

- Errors are printed and the function returns, but the process still exits successfully unless something else fails.

Impact:

- Scripts and test harnesses cannot rely on exit status.

## Low-Severity Findings

### 15. Default filename resolution is not URL-safe in all cases

Location:

- [io.go:17-23](io.go#L17)

Problem:

- `path.Base(originalURL)` is applied to the full URL string, not to a parsed URL path.
- URLs with query strings or trailing slashes can produce unexpected filenames.

Impact:

- Some downloads may be saved under incorrect names.

### 16. Mirror exclusion logic is too broad

Location:

- [mirror.go:37-39](mirror.go#L37)

Problem:

- `strings.Contains(abs, x)` is used for exclusion.
- `-X=/img` would also match unrelated paths such as `/images`, `/myimg`, or even query strings containing `/img`.

Impact:

- Exclusion rules are not precise.

### 17. Mirror reject logic can miss extensions with query strings

Location:

- [mirror.go:33-36](mirror.go#L33)

Problem:

- Suffix checking is done directly on the full URL string.
- A URL ending with `.gif?version=1` will not match `gif` correctly.

Impact:

- `--reject` is unreliable on real sites.

### 18. No tests are present

Observation:

- There are no `_test.go` files in the repository.

Impact:

- Regressions are easy to introduce.
- The mirror and download behaviors are not protected by automated verification.

### 19. Build output name does not match the audit examples

Location:

- [Makefile:1](Makefile#L1)

Problem:

- The produced binary name is `wget-v2`.
- The subject and audit use `./wget`.

Impact:

- Depending on the evaluation flow, this may require manual renaming or a different build command.

## Requirement Coverage Matrix

### Likely Passing or Partially Passing

- Basic single HTTP download: partial
- `-O` custom output filename: partial
- `-P` output directory: partial
- `--rate-limit`: partial
- `-i` input file parsing: partial
- `--mirror`: partial
- `-R`: partial
- `-X`: partial
- `--convert-links`: weak partial

### Likely Failing

- Stop on non-`200 OK` responses
- Exact status display
- Finish timestamp display
- Background download behavior
- Silent/background log structure
- Asynchronous multi-download
- Reliable offline mirrored site behavior
- CSS resource parsing during mirroring
- Subject example using `-P=~/Downloads/`

## Priority Order To Handle In The Project

### Priority 1

- Enforce real HTTP status handling and abort on non-200 responses
- Add exact required reporting format, including `finished at ...`
- Fix exit codes on failure

### Priority 2

- Rework `-B` into a true detached/background execution model
- Prevent progress-bar noise from polluting `wget-log`
- Expand `~` in `-P`

### Priority 3

- Implement true concurrent downloads for `-i`
- Add structured result reporting for multi-download mode

### Priority 4

- Rework mirror logic to:
  - parse CSS resources
  - perform correct offline link rewriting
  - preserve query-based resources safely
  - apply precise reject/exclude matching

### Priority 5

- Stop ignoring filesystem and network errors
- Validate rate-limit input strictly
- Add tests for single download, flags, and mirror filtering

## Build Verification

Static build result:

- `go build ./...` succeeded

## Final Conclusion

The repository is a working prototype, not yet a subject-complete `wget` implementation. It demonstrates the intended feature directions, but several mandatory behaviors from `subject.txt` and `audit.txt` are currently missing or inaccurate. The biggest blockers are incorrect HTTP success handling, non-asynchronous `-i`, non-detached `-B`, incomplete logging/reporting, and a mirror implementation that is too shallow for reliable offline website reproduction.
