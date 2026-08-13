package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type plan struct {
	ResourceChanges []resourceChange `json:"resource_changes"`
	PlannedValues   plannedValues    `json:"planned_values"`
}

type plannedValues struct {
	Outputs map[string]planOutput `json:"outputs"`
}

type planOutput struct {
	Sensitive bool            `json:"sensitive"`
	Value     json.RawMessage `json:"value"`
}

type resourceChange struct {
	Address         string `json:"address"`
	PreviousAddress string `json:"previous_address"`
	Type            string `json:"type"`
	Name            string `json:"name"`
	ProviderName    string `json:"provider_name"`
	Change          change `json:"change"`
}

type change struct {
	Actions         []string        `json:"actions"`
	Before          json.RawMessage `json:"before"`
	After           json.RawMessage `json:"after"`
	BeforeSensitive json.RawMessage `json:"before_sensitive"`
	AfterSensitive  json.RawMessage `json:"after_sensitive"`
}

type row struct {
	id       string
	resource *resourceChange
	title    string
}

type viewer struct {
	app           *tview.Application
	pages         *tview.Pages
	resources     *tview.List
	details       *tview.TextView
	search        *tview.InputField
	propertyFind  *tview.InputField
	outputFind    *tview.InputField
	outputTree    *tview.TreeView
	rightPane     *tview.Pages
	summary       *tview.TextView
	filterStatus  *tview.TextView
	planPath      string
	changes       []resourceChange
	outputs       map[string]planOutput
	rows          []row
	expanded      map[string]bool
	actionFilter  string
	outputMode    bool
	showSensitive bool
	lastModified  time.Time
	reloadPrompt  bool
	fileDialog    bool
	currentDir    string
	pathInput     *tview.InputField
	directories   *tview.List
	files         *tview.List
	dialogStatus  *tview.TextView
	mu            sync.Mutex
}

func main() {
	if len(os.Args) > 2 || (len(os.Args) == 2 && (os.Args[1] == "-h" || os.Args[1] == "--help")) {
		fmt.Fprintln(os.Stderr, "Usage: terraform-plan-viewer [plan.json]")
		os.Exit(2)
	}

	var path string
	if len(os.Args) == 2 {
		var err error
		path, err = filepath.Abs(os.Args[1])
		if err != nil {
			fatal(err)
		}
		if _, err := os.Stat(path); err != nil {
			fatal(fmt.Errorf("plan file: %w", err))
		}
	}

	v := newViewer(path)
	if path != "" {
		if err := v.reload(); err != nil {
			fatal(err)
		}
	} else {
		v.openFileDialog()
	}
	v.watch()
	if err := v.app.Run(); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func newViewer(path string) *viewer {
	v := &viewer{
		app:      tview.NewApplication().EnableMouse(true),
		pages:    tview.NewPages(),
		planPath: path,
		expanded: make(map[string]bool),
	}

	v.resources = tview.NewList().ShowSecondaryText(false)
	v.resources.SetBorder(true).SetTitle(" Resources ")
	v.resources.SetBackgroundColor(tcell.ColorBlack)
	v.resources.SetSelectedBackgroundColor(tcell.ColorDarkSlateGray)
	v.resources.SetSelectedTextColor(tcell.ColorWhite)
	v.resources.SetChangedFunc(func(index int, _, _ string, _ rune) { v.showDetails(index) })

	v.details = tview.NewTextView().SetDynamicColors(true).SetWrap(true).SetScrollable(true)
	v.details.SetBorder(true).SetTitle(" Properties ")
	v.details.SetBackgroundColor(tcell.ColorBlack)
	v.details.SetTextColor(tcell.ColorWhite)

	v.search = tview.NewInputField().SetLabel("Filter: ").SetFieldWidth(0)
	v.search.SetBackgroundColor(tcell.ColorBlack)
	v.search.SetFieldBackgroundColor(tcell.ColorDarkSlateGray)
	v.search.SetChangedFunc(func(string) { v.refreshRows() })
	v.propertyFind = tview.NewInputField().SetLabel("Properties / values: ").SetFieldWidth(0)
	v.propertyFind.SetBackgroundColor(tcell.ColorBlack)
	v.propertyFind.SetFieldBackgroundColor(tcell.ColorDarkSlateGray)
	v.propertyFind.SetChangedFunc(func(string) { v.showDetails(v.resources.GetCurrentItem()) })
	v.outputFind = tview.NewInputField().SetLabel("Outputs: ").SetFieldWidth(0)
	v.outputFind.SetBackgroundColor(tcell.ColorBlack)
	v.outputFind.SetFieldBackgroundColor(tcell.ColorDarkSlateGray)
	v.outputFind.SetChangedFunc(func(string) { v.buildOutputTree() })
	v.outputTree = tview.NewTreeView()
	v.outputTree.SetBorder(true).SetTitle(" Outputs ")
	v.outputTree.SetBackgroundColor(tcell.ColorBlack)
	v.outputTree.SetGraphics(false)
	v.outputTree.SetSelectedFunc(func(node *tview.TreeNode) {
		if len(node.GetChildren()) > 0 {
			node.SetExpanded(!node.IsExpanded())
			v.updateOutputMarkers(v.outputTree.GetRoot())
		}
	})

	v.summary = tview.NewTextView().SetDynamicColors(true)
	v.filterStatus = tview.NewTextView().SetDynamicColors(true)
	header := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(v.search, 1, 0, false).
		AddItem(v.summary, 1, 0, false).
		AddItem(v.filterStatus, 1, 0, false)
	propertyPane := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(v.propertyFind, 1, 0, false).
		AddItem(v.details, 0, 1, false)
	outputPane := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(v.outputFind, 1, 0, false).
		AddItem(v.outputTree, 0, 1, true)
	v.rightPane = tview.NewPages()
	v.rightPane.AddPage("properties", propertyPane, true, true)
	v.rightPane.AddPage("outputs", outputPane, true, false)
	body := tview.NewFlex().
		AddItem(v.resources, 0, 48, true).
		AddItem(v.rightPane, 0, 52, false)
	footer := tview.NewTextView().
		SetDynamicColors(true).
		SetText("[gray]a[-] all  [green]c[-] create  [yellow]u[-] update  [red]d[-] delete  [magenta]r[-] replace  [cyan]h[-] changes  [gray]/[-] resources  [gray]p[-] values  [gray]o[-] outputs  [gray]x[-] sensitive  [gray]f[-] files  [gray]Space[-] expand  [gray]Ctrl-R[-] reload  [gray]q[-] quit")
	root := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(header, 3, 0, false).
		AddItem(body, 0, 1, true).
		AddItem(footer, 1, 0, false)

	v.pages.AddPage("main", root, true, true)
	v.app.SetRoot(v.pages, true).SetFocus(v.resources)
	v.app.SetInputCapture(v.handleInput)
	return v
}

func (v *viewer) handleInput(event *tcell.EventKey) *tcell.EventKey {
	if v.fileDialog {
		switch event.Key() {
		case tcell.KeyEsc:
			v.closeFileDialog()
			return nil
		case tcell.KeyTab:
			v.cycleFileDialogFocus(false)
			return nil
		case tcell.KeyBacktab:
			v.cycleFileDialogFocus(true)
			return nil
		}
		if v.app.GetFocus() == v.pathInput {
			return event
		}
		return event
	}

	if event.Key() == tcell.KeyTab {
		v.cycleMainFocus(false)
		return nil
	}
	if event.Key() == tcell.KeyBacktab {
		v.cycleMainFocus(true)
		return nil
	}

	if v.app.GetFocus() == v.search || v.app.GetFocus() == v.propertyFind || v.app.GetFocus() == v.outputFind {
		switch event.Key() {
		case tcell.KeyEsc:
			if v.app.GetFocus() == v.search {
				v.search.SetText("")
				v.app.SetFocus(v.resources)
			} else {
				v.activeValueFilter().SetText("")
				if v.outputMode {
					v.app.SetFocus(v.outputTree)
				} else {
					v.app.SetFocus(v.details)
				}
			}
			return nil
		case tcell.KeyCtrlR:
			v.reloadWithError()
			return nil
		}
		return event
	}

	switch event.Key() {
	case tcell.KeyCtrlF:
		v.app.SetFocus(v.search)
		return nil
	case tcell.KeyCtrlR:
		v.reloadWithError()
		return nil
	case tcell.KeyEsc:
		v.actionFilter = ""
		v.search.SetText("")
		v.refreshRows()
		return nil
	case tcell.KeyLeft:
		if v.app.GetFocus() == v.outputTree {
			v.setOutputExpanded(false)
			return nil
		}
		v.setExpanded(false)
		return nil
	case tcell.KeyRight:
		if v.app.GetFocus() == v.outputTree {
			v.setOutputExpanded(true)
			return nil
		}
		v.setExpanded(true)
		return nil
	case tcell.KeyRune:
		switch event.Rune() {
		case 'q':
			v.app.Stop()
		case 'f':
			v.openFileDialog()
		case 'p':
			v.app.SetFocus(v.activeValueFilter())
		case 'o':
			v.toggleOutputMode()
		case 'x':
			v.showSensitive = !v.showSensitive
			v.showDetails(v.resources.GetCurrentItem())
		case '/':
			v.app.SetFocus(v.search)
		case ' ':
			if v.app.GetFocus() == v.outputTree {
				return event
			}
			v.toggleSelected()
		case 'a':
			v.setActionFilter("")
		case 'c':
			v.setActionFilter("create")
		case 'u':
			v.setActionFilter("update")
		case 'd':
			v.setActionFilter("delete")
		case 'r':
			v.setActionFilter("replace")
		case 'h':
			v.setActionFilter("changes")
		default:
			return event
		}
		return nil
	}
	return event
}

func (v *viewer) reload() error {
	return v.loadPlan(v.planPath)
}

func (v *viewer) loadPlan(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var loaded plan
	if err := json.Unmarshal(data, &loaded); err != nil {
		return err
	}
	sort.Slice(loaded.ResourceChanges, func(i, j int) bool {
		return loaded.ResourceChanges[i].Address < loaded.ResourceChanges[j].Address
	})
	v.changes = loaded.ResourceChanges
	v.outputs = loaded.PlannedValues.Outputs
	v.planPath = path
	if info, err := os.Stat(path); err == nil {
		v.lastModified = info.ModTime()
	}
	v.updateSummary()
	v.refreshRows()
	return nil
}

func (v *viewer) openFileDialog() {
	v.fileDialog = true
	if v.planPath == "" {
		v.currentDir, _ = os.Getwd()
	} else {
		v.currentDir = filepath.Dir(v.planPath)
	}
	v.pathInput = tview.NewInputField().SetLabel("Path: ").SetFieldWidth(0)
	v.pathInput.SetBackgroundColor(tcell.ColorBlack)
	v.pathInput.SetFieldBackgroundColor(tcell.ColorDarkSlateGray)
	v.pathInput.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			v.openTypedPath()
		}
	})
	v.directories = tview.NewList().ShowSecondaryText(false)
	v.directories.SetBorder(true).SetTitle(" Directories ")
	v.directories.SetBackgroundColor(tcell.ColorBlack)
	v.files = tview.NewList().ShowSecondaryText(false)
	v.files.SetBorder(true).SetTitle(" Files ")
	v.files.SetBackgroundColor(tcell.ColorBlack)
	v.dialogStatus = tview.NewTextView().SetDynamicColors(true)
	v.dialogStatus.SetTextColor(tcell.ColorWhite)

	browser := tview.NewFlex().
		AddItem(v.directories, 0, 1, false).
		AddItem(v.files, 0, 1, false)
	help := tview.NewTextView().SetText("[gray]Enter[-] open typed path  [gray]Esc[-] close  Select a folder or file to open it")
	dialog := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(v.pathInput, 1, 0, true).
		AddItem(browser, 0, 1, false).
		AddItem(v.dialogStatus, 1, 0, false).
		AddItem(help, 1, 0, false)
	dialog.SetBorder(true).SetTitle(" Load Terraform Plan ")
	v.pages.AddPage("files", dialog, true, true)
	v.refreshFileBrowser()
	v.app.SetFocus(v.pathInput)
}

func (v *viewer) closeFileDialog() {
	v.fileDialog = false
	v.pages.RemovePage("files")
	v.app.SetFocus(v.resources)
}

func (v *viewer) cycleFileDialogFocus(reverse bool) {
	views := []tview.Primitive{v.pathInput, v.directories, v.files}
	current := 0
	for index, view := range views {
		if v.app.GetFocus() == view {
			current = index
			break
		}
	}
	if reverse {
		current = (current + len(views) - 1) % len(views)
	} else {
		current = (current + 1) % len(views)
	}
	v.app.SetFocus(views[current])
}

func (v *viewer) openTypedPath() {
	path := strings.TrimSpace(v.pathInput.GetText())
	if path == "" {
		path = v.currentDir
	}
	path = expandPath(path)
	if !filepath.IsAbs(path) {
		path = filepath.Join(v.currentDir, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		v.dialogStatus.SetText("[red]" + err.Error() + "[-]")
		return
	}
	if info.IsDir() {
		v.currentDir = path
		v.refreshFileBrowser()
		return
	}
	v.loadSelectedFile(path)
}

func (v *viewer) refreshFileBrowser() {
	v.pathInput.SetText(v.currentDir)
	v.directories.Clear()
	v.files.Clear()
	entries, err := os.ReadDir(v.currentDir)
	if err != nil {
		v.dialogStatus.SetText("[red]" + err.Error() + "[-]")
		return
	}
	v.dialogStatus.SetText(fmt.Sprintf("[gray]%d entries in %s[-]", len(entries), v.currentDir))
	parent := filepath.Dir(v.currentDir)
	if parent != v.currentDir {
		v.directories.AddItem("..", "", 0, func() {
			v.currentDir = parent
			v.refreshFileBrowser()
		})
	}
	for _, entry := range entries {
		entry := entry
		path := filepath.Join(v.currentDir, entry.Name())
		if entry.IsDir() {
			v.directories.AddItem("DIR "+entry.Name(), "", 0, func() {
				v.currentDir = path
				v.refreshFileBrowser()
			})
		} else if strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			v.files.AddItem(entry.Name(), "", 0, func() {
				v.loadSelectedFile(path)
			})
		}
	}
}

func (v *viewer) loadSelectedFile(path string) {
	if err := v.loadPlan(path); err != nil {
		v.dialogStatus.SetText("[red]Could not load " + filepath.Base(path) + ": " + err.Error() + "[-]")
		return
	}
	v.closeFileDialog()
}

func (v *viewer) reloadWithError() {
	if err := v.reload(); err != nil {
		v.showModal("Could not reload plan", err.Error(), "OK", func() {})
	}
}

func (v *viewer) updateSummary() {
	var create, update, deleteCount, replace, changed int
	for _, resource := range v.changes {
		if !resource.noOp() {
			changed++
		}
		if resource.replace() {
			replace++
		} else if resource.hasAction("create") {
			create++
		} else if resource.hasAction("update") {
			update++
		} else if resource.hasAction("delete") {
			deleteCount++
		}
	}
	v.summary.SetText(fmt.Sprintf("[green]Create %d[-]  [yellow]Update %d[-]  [red]Delete %d[-]  [magenta]Replace %d[-]  Changes %d / Total %d",
		create, update, deleteCount, replace, changed, len(v.changes)))
}

func (v *viewer) refreshRows() {
	previous := v.selectedID()
	filtered := make([]resourceChange, 0, len(v.changes))
	for _, resource := range v.changes {
		if v.matches(resource) {
			filtered = append(filtered, resource)
		}
	}
	v.ensureDefaultExpansion(filtered)
	v.rows = nil

	modules := make(map[string][]resourceChange)
	for _, resource := range filtered {
		modules[resource.moduleAddress()] = append(modules[resource.moduleAddress()], resource)
	}
	moduleNames := sortedKeys(modules)
	sort.Slice(moduleNames, func(i, j int) bool {
		if moduleNames[i] == "root" {
			return true
		}
		if moduleNames[j] == "root" {
			return false
		}
		return moduleNames[i] < moduleNames[j]
	})
	for _, module := range moduleNames {
		moduleID := "module:" + module
		name := "Root Module"
		if module != "root" {
			name = displayModule(module) + " Module"
		}
		v.addGroup(moduleID, name, 0, len(modules[module]))
		if !v.expanded[moduleID] {
			continue
		}
		types := make(map[string][]resourceChange)
		for _, resource := range modules[module] {
			types[resource.Type] = append(types[resource.Type], resource)
		}
		for _, resourceType := range sortedKeys(types) {
			typeID := moduleID + ":type:" + resourceType
			v.addGroup(typeID, resourceType, 1, len(types[resourceType]))
			if !v.expanded[typeID] {
				continue
			}
			iteratorGroups := make(map[string][]resourceChange)
			for _, resource := range types[resourceType] {
				iteratorGroups[iteratorBase(resource.Address)] = append(iteratorGroups[iteratorBase(resource.Address)], resource)
			}
			for _, base := range sortedKeys(iteratorGroups) {
				group := iteratorGroups[base]
				if len(group) == 1 {
					v.addResource(group[0], 2)
					continue
				}
				iteratorID := typeID + ":iterator:" + base
				v.addGroup(iteratorID, fmt.Sprintf("%s (%d instances)", base, len(group)), 2, len(group))
				if v.expanded[iteratorID] {
					for _, resource := range group {
						v.addResource(resource, 3)
					}
				}
			}
		}
	}

	v.resources.Clear()
	selected := 0
	for index, item := range v.rows {
		current := item
		v.resources.AddItem(current.title, "", 0, func() {
			if current.resource == nil {
				v.toggleRow(current)
			}
		})
		if current.id == previous {
			selected = index
		}
	}
	if len(v.rows) > 0 {
		v.resources.SetCurrentItem(selected)
	} else {
		v.details.SetText("No resources match the selected filter.")
	}
	filter := v.actionFilter
	if filter == "" {
		filter = "all"
	}
	v.filterStatus.SetText(fmt.Sprintf("Filter: [cyan]%s[-], search: %s", filter, strings.TrimSpace(v.search.GetText())))
	v.showDetails(selected)
}

func (v *viewer) addGroup(id, name string, depth, count int) {
	marker := "▶"
	if v.expanded[id] {
		marker = "▼"
	}
	v.rows = append(v.rows, row{id: id, title: fmt.Sprintf("%s[white]%s %s [gray][%d][-]", strings.Repeat("  ", depth), marker, name, count)})
}

func (v *viewer) addResource(resource resourceChange, depth int) {
	color := actionColor(resource)
	title := fmt.Sprintf("%s[%s]%s %s  [%s]%s[-]", strings.Repeat("  ", depth), color, actionGlyph(resource), resource.Address, color, tview.Escape("["+resource.actionText()+"]"))
	v.rows = append(v.rows, row{id: "resource:" + resource.Address, resource: &resource, title: title})
}

func (v *viewer) selectedID() string {
	index := v.resources.GetCurrentItem()
	if index >= 0 && index < len(v.rows) {
		return v.rows[index].id
	}
	return ""
}

func (v *viewer) selectedRow() *row {
	index := v.resources.GetCurrentItem()
	if index < 0 || index >= len(v.rows) {
		return nil
	}
	return &v.rows[index]
}

func (v *viewer) toggleSelected() {
	if item := v.selectedRow(); item != nil && item.resource == nil {
		v.toggleRow(*item)
	}
}

func (v *viewer) toggleRow(item row) {
	v.expanded[item.id] = !v.expanded[item.id]
	v.refreshRows()
}

func (v *viewer) setExpanded(expanded bool) {
	if item := v.selectedRow(); item != nil && item.resource == nil && v.expanded[item.id] != expanded {
		v.expanded[item.id] = expanded
		v.refreshRows()
	}
}

func (v *viewer) setActionFilter(filter string) {
	v.actionFilter = filter
	v.refreshRows()
}

func (v *viewer) toggleOutputMode() {
	v.outputMode = !v.outputMode
	if v.outputMode {
		v.rightPane.SwitchToPage("outputs")
		v.buildOutputTree()
		v.app.SetFocus(v.outputTree)
	} else {
		v.rightPane.SwitchToPage("properties")
		v.app.SetFocus(v.details)
	}
	v.showDetails(v.resources.GetCurrentItem())
}

func (v *viewer) activeValueFilter() *tview.InputField {
	if v.outputMode {
		return v.outputFind
	}
	return v.propertyFind
}

func (v *viewer) cycleMainFocus(reverse bool) {
	current := 0
	focus := v.app.GetFocus()
	switch focus {
	case v.resources:
		current = 1
	case v.propertyFind, v.details, v.outputFind, v.outputTree:
		current = 2
	}
	if reverse {
		current = (current + 2) % 3
	} else {
		current = (current + 1) % 3
	}
	switch current {
	case 0:
		v.app.SetFocus(v.search)
	case 1:
		v.app.SetFocus(v.resources)
	case 2:
		if v.outputMode {
			v.app.SetFocus(v.outputTree)
		} else {
			v.app.SetFocus(v.details)
		}
	}
}

func (v *viewer) ensureDefaultExpansion(resources []resourceChange) {
	if len(v.expanded) != 0 {
		return
	}
	for _, resource := range resources {
		module := resource.moduleAddress()
		v.expanded["module:"+module] = true
		v.expanded["module:"+module+":type:"+resource.Type] = true
	}
}

func (v *viewer) matches(resource resourceChange) bool {
	search := strings.ToLower(strings.TrimSpace(v.search.GetText()))
	if search != "" && !strings.Contains(strings.ToLower(resource.Address+" "+resource.Type+" "+resource.Name+" "+resource.ProviderName), search) {
		return false
	}
	switch v.actionFilter {
	case "":
		return true
	case "changes":
		return !resource.noOp()
	case "replace":
		return resource.replace()
	case "create", "delete":
		return resource.hasAction(v.actionFilter) && !resource.replace()
	default:
		return resource.hasAction(v.actionFilter)
	}
}

func (v *viewer) showDetails(index int) {
	if v.outputMode {
		v.buildOutputTree()
		return
	}
	if index < 0 || index >= len(v.rows) || v.rows[index].resource == nil {
		v.details.SetText("[white]Select a resource to inspect its properties.[-]\n\nUse Space or Left/Right to collapse or expand groups.\nThe resource and property panes support keyboard and mouse scrolling.")
		return
	}
	resource := *v.rows[index].resource
	var output strings.Builder
	sensitiveState := "masked; press x to reveal"
	if v.showSensitive {
		sensitiveState = "visible; press x to mask"
	}
	fmt.Fprintf(&output, "[white]%s[-]\nType:     %s\nProvider: %s\nActions:  %s\nSensitive values: %s\n\n", resource.Address, resource.Type, resource.ProviderName, resource.actionText(), sensitiveState)
	if resource.PreviousAddress != "" {
		fmt.Fprintf(&output, "Moved from: %s\n\n", resource.PreviousAddress)
	}
	before, err := asObject(resource.Change.Before)
	if err != nil {
		v.details.SetText("Unable to parse resource properties: " + err.Error())
		return
	}
	after, err := asObject(resource.Change.After)
	if err != nil {
		v.details.SetText("Unable to parse resource properties: " + err.Error())
		return
	}
	keys := make(map[string]bool)
	for key := range before {
		keys[key] = true
	}
	for key := range after {
		keys[key] = true
	}
	propertyNames := sortedKeys(keys)
	sort.SliceStable(propertyNames, func(i, j int) bool {
		return !jsonEqual(before[propertyNames[i]], after[propertyNames[i]]) && jsonEqual(before[propertyNames[j]], after[propertyNames[j]])
	})
	propertyFilter := strings.ToLower(strings.TrimSpace(v.propertyFind.GetText()))
	matches := 0
	for _, property := range propertyNames {
		if property == "output" {
			continue
		}
		beforeValue, beforeExists := before[property]
		afterValue, afterExists := after[property]
		sensitive := resource.propertySensitive(property)
		if propertyFilter != "" &&
			!strings.Contains(strings.ToLower(property), propertyFilter) &&
			(sensitive && !v.showSensitive ||
				(!strings.Contains(strings.ToLower(string(beforeValue)), propertyFilter) &&
					!strings.Contains(strings.ToLower(string(afterValue)), propertyFilter))) {
			continue
		}
		matches++
		if beforeExists && afterExists && jsonEqual(beforeValue, afterValue) {
			fmt.Fprintf(&output, "[aqua]    %s[-]\n[white]= %s[-]\n\n", property, v.formatSensitiveValue(afterValue, true, sensitive))
			continue
		}
		fmt.Fprintf(&output, "[aqua]--- %s (changed)[-]\n[red]- %s[-]\n[green]+ %s[-]\n\n", property, v.formatSensitiveValue(beforeValue, beforeExists, sensitive), v.formatSensitiveValue(afterValue, afterExists, sensitive))
	}
	if matches == 0 {
		output.WriteString("[gray]No properties match the current property filter.[-]\n")
	}
	v.details.SetText(output.String())
}

func (v *viewer) buildOutputTree() {
	root := outputNode("Outputs", tcell.ColorWhite).SetExpanded(true)
	filter := strings.ToLower(strings.TrimSpace(v.outputFind.GetText()))
	for _, name := range sortedKeys(v.outputs) {
		value := v.outputs[name]
		if filter != "" &&
			!strings.Contains(strings.ToLower(name), filter) &&
			(value.Sensitive && !v.showSensitive ||
				!strings.Contains(strings.ToLower(string(value.Value)), filter)) {
			continue
		}
		node := outputNode(name, tcell.ColorWhite)
		v.addOutputValue(node, value.Value, value.Sensitive)
		root.AddChild(node)
	}
	if len(root.GetChildren()) == 0 {
		root.AddChild(outputNode("No outputs match the current filter.", tcell.ColorGray))
	}
	v.updateOutputMarkers(root)
	v.outputTree.SetRoot(root).SetCurrentNode(root)
}

func (v *viewer) addOutputValue(node *tview.TreeNode, raw json.RawMessage, sensitive bool) {
	if sensitive && !v.showSensitive {
		setOutputNodeText(node, outputNodeText(node)+" (sensitive)")
		node.SetColor(tcell.ColorYellow)
		return
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) == nil {
		node.SetExpanded(false)
		for _, key := range sortedKeys(object) {
			child := outputNode(key, tcell.ColorWhite)
			v.addOutputValue(child, object[key], false)
			node.AddChild(child)
		}
		return
	}
	var array []json.RawMessage
	if json.Unmarshal(raw, &array) == nil {
		node.SetExpanded(false)
		for index, value := range array {
			child := outputNode(fmt.Sprintf("[%d]", index), tcell.ColorWhite)
			v.addOutputValue(child, value, false)
			node.AddChild(child)
		}
		return
	}
	setOutputNodeText(node, outputNodeText(node)+": "+formatJSONOrNone(raw, len(raw) != 0))
}

func outputNode(text string, color tcell.Color) *tview.TreeNode {
	return tview.NewTreeNode(text).SetColor(color).SetReference(text)
}

func setOutputNodeText(node *tview.TreeNode, text string) {
	node.SetReference(text).SetText(text)
}

func outputNodeText(node *tview.TreeNode) string {
	if text, ok := node.GetReference().(string); ok {
		return text
	}
	return node.GetText()
}

func (v *viewer) updateOutputMarkers(node *tview.TreeNode) {
	text := outputNodeText(node)
	if len(node.GetChildren()) > 0 {
		marker := "▶"
		if node.IsExpanded() {
			marker = "▼"
		}
		node.SetText(marker + " " + text)
	} else {
		node.SetText("  " + text)
	}
	for _, child := range node.GetChildren() {
		v.updateOutputMarkers(child)
	}
}

func (v *viewer) setOutputExpanded(expanded bool) {
	node := v.outputTree.GetCurrentNode()
	if node == nil || len(node.GetChildren()) == 0 || node.IsExpanded() == expanded {
		return
	}
	node.SetExpanded(expanded)
	v.updateOutputMarkers(v.outputTree.GetRoot())
}

func (v *viewer) watch() {
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			path := v.planPath
			if path == "" {
				continue
			}
			info, err := os.Stat(path)
			if err != nil || !info.ModTime().After(v.lastModified) {
				continue
			}
			v.lastModified = info.ModTime()
			v.mu.Lock()
			if v.reloadPrompt {
				v.mu.Unlock()
				continue
			}
			v.reloadPrompt = true
			v.mu.Unlock()
			v.app.QueueUpdateDraw(func() {
				v.showModal("Plan changed", filepath.Base(v.planPath)+" changed. Reload it?", "Reload", func() {
					v.mu.Lock()
					v.reloadPrompt = false
					v.mu.Unlock()
					v.reloadWithError()
				}, "Keep", func() {
					v.mu.Lock()
					v.reloadPrompt = false
					v.mu.Unlock()
				})
			})
		}
	}()
}

func (v *viewer) showModal(title, message string, first string, firstAction func(), remaining ...interface{}) {
	buttons := []string{first}
	actions := []func(){firstAction}
	for index := 0; index < len(remaining); index += 2 {
		buttons = append(buttons, remaining[index].(string))
		actions = append(actions, remaining[index+1].(func()))
	}
	modal := tview.NewModal().SetText(message).AddButtons(buttons).SetDoneFunc(func(index int, _ string) {
		v.pages.RemovePage("modal")
		actions[index]()
		v.app.SetFocus(v.resources)
	})
	modal.SetBorder(true).SetTitle(" " + title + " ")
	v.pages.AddPage("modal", modal, true, true)
	v.app.SetFocus(modal)
}

func (resource resourceChange) hasAction(action string) bool {
	for _, item := range resource.Change.Actions {
		if item == action {
			return true
		}
	}
	return false
}

func (resource resourceChange) noOp() bool {
	return len(resource.Change.Actions) == 1 && resource.Change.Actions[0] == "no-op"
}

func (resource resourceChange) replace() bool {
	return resource.hasAction("replace") || (resource.hasAction("create") && resource.hasAction("delete"))
}

func (resource resourceChange) actionText() string {
	return strings.Join(resource.Change.Actions, ", ")
}

func (resource resourceChange) moduleAddress() string {
	parts := strings.Split(resource.Address, ".")
	var modules []string
	for index := 0; index+1 < len(parts) && parts[index] == "module"; index += 2 {
		modules = append(modules, parts[index], parts[index+1])
	}
	if len(modules) == 0 {
		return "root"
	}
	return strings.Join(modules, ".")
}

func (resource resourceChange) propertySensitive(property string) bool {
	return sensitiveProperty(resource.Change.BeforeSensitive, property) ||
		sensitiveProperty(resource.Change.AfterSensitive, property)
}

func sensitiveProperty(raw json.RawMessage, property string) bool {
	var values map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &values) != nil {
		return false
	}
	return sensitiveMarker(values[property])
}

func sensitiveMarker(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var value bool
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) == nil {
		for _, child := range object {
			if sensitiveMarker(child) {
				return true
			}
		}
		return false
	}
	var array []json.RawMessage
	if json.Unmarshal(raw, &array) == nil {
		for _, child := range array {
			if sensitiveMarker(child) {
				return true
			}
		}
	}
	return false
}

func asObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]json.RawMessage{}, nil
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func jsonEqual(left, right json.RawMessage) bool {
	return string(left) == string(right)
}

func formatJSON(value json.RawMessage) string {
	var output bytes.Buffer
	if err := json.Indent(&output, value, "", "  "); err != nil {
		return string(value)
	}
	return output.String()
}

func formatJSONOrNone(value json.RawMessage, exists bool) string {
	if !exists {
		return "(none)"
	}
	return formatJSON(value)
}

func (v *viewer) formatSensitiveValue(value json.RawMessage, exists, sensitive bool) string {
	if sensitive && !v.showSensitive {
		return "(sensitive; press x to reveal)"
	}
	return formatJSONOrNone(value, exists)
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func iteratorBase(address string) string {
	if index := strings.LastIndex(address, "["); index > 0 && strings.HasSuffix(address, "]") {
		return address[:index]
	}
	return address
}

func actionGlyph(resource resourceChange) string {
	switch {
	case resource.replace():
		return "↻"
	case resource.hasAction("create"):
		return "+"
	case resource.hasAction("delete"):
		return "-"
	case resource.hasAction("update"):
		return "~"
	default:
		return "·"
	}
}

func actionColor(resource resourceChange) string {
	switch {
	case resource.replace():
		return "magenta"
	case resource.hasAction("create"):
		return "green"
	case resource.hasAction("delete"):
		return "red"
	case resource.hasAction("update"):
		return "yellow"
	default:
		return "gray"
	}
}

func displayModule(address string) string {
	parts := strings.Split(address, ".")
	var names []string
	for index := 1; index < len(parts); index += 2 {
		names = append(names, parts[index])
	}
	return strings.Join(names, " > ")
}

func expandPath(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}
