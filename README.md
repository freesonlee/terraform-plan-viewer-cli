# Terraform Plan Viewer CLI

A native terminal UI for exploring Terraform plan JSON files. It helps you review resource creates, updates, deletes, replacements, property differences, and Terraform outputs without leaving the terminal.

## Install and start

Download the Linux binary for your CPU architecture from the [GitHub Releases](../../releases) page, then make it executable:

```bash
chmod +x terraform-plan-viewer_linux_amd64
./terraform-plan-viewer_linux_amd64 plan.json
```

Create `plan.json` from a Terraform plan with:

```bash
terraform plan -out=tfplan
terraform show -json tfplan > plan.json
```

You can also start without a file. Press `f` to browse for a plan, or type a path in the file picker:

```bash
./terraform-plan-viewer_linux_amd64
```

You may rename the downloaded binary to a shorter local command, such as `tpv`:

```bash
mv terraform-plan-viewer_linux_amd64 tpv
./tpv plan.json
```

## Navigation

- `a`, `c`, `u`, `d`, `r`, `h`: show all, creates, updates, deletes, replacements, or all changed resources.
- `/`: search resources by address, type, name, or provider.
- `p`: search property names and values in the right pane.
- `o`: switch between resource properties and Terraform outputs.
- `x`: reveal or mask sensitive values.
- `Space`, Left, Right: collapse or expand resource and output groups.
- `f`: open the plan file picker.
- `Ctrl-R`: reload the current plan.

The viewer watches the active plan file and prompts before reloading it after a change.
