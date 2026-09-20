'use strict';
'require baseclass';

// luci-app-wellboard: the menu "Open Web UI" node.
// WellBoard serves its own full SPA on port 8090; LuCI only needs to
// bounce the browser there. Port is read from UCI wellboard.main.port.

return baseclass.extend({
	title: _('WellBoard'),

	load: function () { return Promise.resolve(null); },

	render: function () {
		var port = 8090;
		// L.env.sessiondata/uci not loaded for a redirect page: read the
		// port from the embedded env if present, else use default.
		try {
			if (L && L.env && L.env.wellboardPort) {
				port = L.env.wellboardPort;
			}
		} catch (e) { /* fall back to default */ }

		var host = (window.location && window.location.hostname) || '127.0.0.1';
		window.location.replace('http://' + host + ':' + port + '/');
		return E('p', {}, _('Redirecting to the WellBoard web UI on port %d…').format(port));
	}
});
