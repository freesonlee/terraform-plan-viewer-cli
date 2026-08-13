# Terraform Plan Viewer CLI

A native Linux terminal UI for Terraform JSON plans. Build once and run the resulting binary without a .NET runtime.

```bash
CGO_ENABLED=0 go build -o terraform-plan-viewer ./cmd/terraform-plan-viewer
./terraform-plan-viewer tfplan-dev.json
```

Run `./terraform-plan-viewer` without an argument to open the file browser immediately.

Use `a`, `c`, `u`, `d`, `r`, or `h` to filter all, create, update, delete, replace, or changed resources. Press `/` to enter the filter, `Space` or left/right to expand groups, and `Ctrl-R` to reload. The app prompts before reloading a changed plan file.

## Releases

Pushing a version tag creates a GitHub Release with statically linked Linux binaries for `amd64` and `arm64`.

```bash
git tag v0.1.0
git push origin v0.1.0
```
