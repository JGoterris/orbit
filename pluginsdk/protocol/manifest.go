package protocol

// ManifestFile is the name of the manifest at the root of a plugin.
const ManifestFile = "orbit-plugin.yaml"

// Manifest is orbit-plugin.yaml. What a plugin contributes is declared here, not at run time, so orbit can draw
// the tabs, keys and palette entries before the plugin process answers and the user can see what a plugin adds
// before installing it.
type Manifest struct {
	Name        string `yaml:"name" json:"name"`
	Version     string `yaml:"version" json:"version"`
	API         int    `yaml:"api" json:"api"`
	MinOrbit    string `yaml:"minOrbit,omitempty" json:"minOrbit,omitempty"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// Run is the long-lived process orbit talks to; without it the plugin only has a CLI
	Run *Command `yaml:"run,omitempty" json:"run,omitempty"`
	// CLI is what `orbit <name> …` runs
	CLI *Command `yaml:"cli,omitempty" json:"cli,omitempty"`
	// Events lists what the plugin wants to be told: state, config, log
	Events      []string    `yaml:"events,omitempty" json:"events,omitempty"`
	Contributes Contributes `yaml:"contributes,omitempty" json:"contributes"`
}

// Command is an argv (no shell). Relative paths starting with ./ are in the folder of the plugin. Windows
// replaces Command on Windows.
type Command struct {
	Command []string `yaml:"command" json:"command"`
	Windows []string `yaml:"windows,omitempty" json:"windows,omitempty"`
}

// Contributes is what a plugin adds to orbit.
type Contributes struct {
	Views           []View        `yaml:"views,omitempty" json:"views,omitempty"`
	Actions         []Action      `yaml:"actions,omitempty" json:"actions,omitempty"`
	ServiceSections []SectionDef  `yaml:"serviceSections,omitempty" json:"serviceSections,omitempty"`
	ServiceColumn   *ColumnDef    `yaml:"serviceColumn,omitempty" json:"serviceColumn,omitempty"`
	HeaderBadges    bool          `yaml:"headerBadges,omitempty" json:"headerBadges,omitempty"`
	ServiceTypes    []ServiceType `yaml:"serviceTypes,omitempty" json:"serviceTypes,omitempty"`
}

// Sidebar values of a view.
const (
	SidebarNone     = "none"
	SidebarServices = "services"
)

// View is a tab of the header.
type View struct {
	ID    string `yaml:"id" json:"id"`
	Title string `yaml:"title" json:"title"`
	// Sidebar: "none" (default) or "services", the list of services on the left like the Graph view
	Sidebar string `yaml:"sidebar,omitempty" json:"sidebar,omitempty"`
	Panes   []Pane `yaml:"panes" json:"panes"`
}

// Pane is a panel of a view, laid out left to right in the order they are declared.
type Pane struct {
	ID     string `yaml:"id" json:"id"`
	Widget string `yaml:"widget" json:"widget"`
	Title  string `yaml:"title,omitempty" json:"title,omitempty"`
	// Weight is the share of the width (default 1)
	Weight int `yaml:"weight,omitempty" json:"weight,omitempty"`
	// Bind is another pane of the view: this one is asked again whenever the selection of that one changes
	Bind string `yaml:"bind,omitempty" json:"bind,omitempty"`
	// Follow keeps a text pane at its end
	Follow bool `yaml:"follow,omitempty" json:"follow,omitempty"`
	// Wrap breaks the long lines of a text pane instead of cutting them (a detail always wraps)
	Wrap bool `yaml:"wrap,omitempty" json:"wrap,omitempty"`
	// Sized makes orbit ask again for the data of the pane when its width changes: what it draws depends on it
	// (Context.Width)
	Sized bool `yaml:"sized,omitempty" json:"sized,omitempty"`
}

// Action is a command: it has an entry in the palette and, with a key, a shortcut.
type Action struct {
	ID    string `yaml:"id" json:"id"`
	Title string `yaml:"title" json:"title"`
	Key   string `yaml:"key,omitempty" json:"key,omitempty"`
	Scope Scope  `yaml:"scope,omitempty" json:"scope,omitempty"`
	// Confirm asks before running; {item} and {service} in the title are replaced
	Confirm *Confirm `yaml:"confirm,omitempty" json:"confirm,omitempty"`
	Inputs  []Input  `yaml:"inputs,omitempty" json:"inputs,omitempty"`
	// Item says the action is about the selected item of its pane (scope.pane): while there is none, it is not
	// offered and does not run
	Item bool `yaml:"item,omitempty" json:"item,omitempty"`
}

// Scope says where an action is available: the whole app (empty), a view, a pane of it, or one entry per service.
type Scope struct {
	View    string `yaml:"view,omitempty" json:"view,omitempty"`
	Pane    string `yaml:"pane,omitempty" json:"pane,omitempty"`
	Service bool   `yaml:"service,omitempty" json:"service,omitempty"`
}

// Confirm is the confirmation dialog of an action.
type Confirm struct {
	Title  string `yaml:"title" json:"title"`
	Danger bool   `yaml:"danger,omitempty" json:"danger,omitempty"`
}

// Input kinds.
const (
	InputText = "text"
	InputPick = "pick"
)

// Input is something an action asks the user for before it runs.
type Input struct {
	Name    string   `yaml:"name" json:"name"`
	Kind    string   `yaml:"kind" json:"kind"`
	Title   string   `yaml:"title,omitempty" json:"title,omitempty"`
	Default string   `yaml:"default,omitempty" json:"default,omitempty"`
	Options []string `yaml:"options,omitempty" json:"options,omitempty"`
	// OptionsFrom asks the plugin (action.options) for the entries of a pick
	OptionsFrom bool `yaml:"optionsFrom,omitempty" json:"optionsFrom,omitempty"`
}

// SectionDef is a block the plugin adds to the detail of a service.
type SectionDef struct {
	ID    string `yaml:"id" json:"id"`
	Title string `yaml:"title" json:"title"`
}

// ColumnDef is the column the plugin adds to the list of services; its content are the badges the plugin sets.
type ColumnDef struct {
	ID    string `yaml:"id" json:"id"`
	Title string `yaml:"title,omitempty" json:"title,omitempty"`
	Width int    `yaml:"width,omitempty" json:"width,omitempty"`
}

// ServiceType is a `type:` the plugin provides for the services of orbit.yaml.
type ServiceType struct {
	Name string `yaml:"name" json:"name"`
	// Badge is the short tag shown next to services of this type (default: the name, cut to 4 letters)
	Badge string `yaml:"badge,omitempty" json:"badge,omitempty"`
}
