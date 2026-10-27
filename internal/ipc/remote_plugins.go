package ipc

import (
	"encoding/json"
	"sync"

	"github.com/JGoterris/orbit/internal/core/supervisor"
	"github.com/JGoterris/orbit/internal/plugins"
	"github.com/JGoterris/orbit/pluginsdk/protocol"
)

// remotePlugins is the plugins of a daemon as the interface sees them: the state it mirrors comes with the
// notifications, and what needs the plugin (data, actions) is asked over the socket.
type remotePlugins struct {
	client *Client

	mu     sync.Mutex
	infos  []plugins.Info
	badges map[string][]plugins.TaggedBadge

	bus supervisor.Bus[plugins.Update]
}

var _ plugins.Host = (*remotePlugins)(nil)

func newRemotePlugins(c *Client, infos []plugins.Info, badges map[string][]plugins.TaggedBadge) *remotePlugins {
	return &remotePlugins{client: c, infos: infos, badges: badges}
}

// reload asks the daemon for the plugins as they are now. It is done after subscribing: a plugin may have changed
// between the hello and the subscription, and nobody would have said.
func (r *remotePlugins) reload() error {
	var st PluginsState
	if err := r.client.Call(MethodPlugins, nil, &st); err != nil {
		return err
	}
	r.mu.Lock()
	r.infos, r.badges = st.Plugins, st.Badges
	r.mu.Unlock()
	return nil
}

func (r *remotePlugins) apply(raw json.RawMessage) {
	var n PluginNotice
	if json.Unmarshal(raw, &n) != nil {
		return
	}
	r.mu.Lock()
	switch n.Kind {
	case plugins.UpdateBadges:
		r.badges = n.Badges
	case plugins.UpdateStatus:
		r.infos = n.Plugins
	}
	r.mu.Unlock()
	r.bus.Emit(n.Update)
}

func (r *remotePlugins) Plugins() []plugins.Info {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.infos
}

func (r *remotePlugins) Badges() map[string][]plugins.TaggedBadge {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.badges
}

func (r *remotePlugins) OnUpdate(fn func(plugins.Update)) (cancel func()) { return r.bus.On(fn) }

func (r *remotePlugins) call(method, plugin string, data, out any) error {
	return r.client.Call(method, map[string]any{"plugin": plugin, "data": data}, out)
}

func (r *remotePlugins) Data(plugin string, p protocol.ViewDataParams) (protocol.ViewData, error) {
	var out protocol.ViewData
	return out, r.call(MethodPluginData, plugin, p, &out)
}

func (r *remotePlugins) Invoke(plugin string, p protocol.InvokeParams) (protocol.InvokeResult, error) {
	var out protocol.InvokeResult
	return out, r.call(MethodPluginAction, plugin, p, &out)
}

func (r *remotePlugins) Options(plugin string, p protocol.OptionsParams) ([]protocol.Option, error) {
	var out []protocol.Option
	return out, r.call(MethodPluginOptions, plugin, p, &out)
}

func (r *remotePlugins) Sections(plugin string, p protocol.SectionsParams) ([]protocol.Section, error) {
	var out []protocol.Section
	return out, r.call(MethodPluginSections, plugin, p, &out)
}
