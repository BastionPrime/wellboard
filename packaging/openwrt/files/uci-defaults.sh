# WellBoard uci-defaults script, run once at first boot after install.
# Creates the state directory with restrictive permissions and enables
# the service. opkg deletes /etc/uci-defaults entries after a
# successful run; exit 0 is mandatory.

[ -d /etc/wellboard ] || {
	mkdir -p /etc/wellboard
	chmod 0700 /etc/wellboard
}

# Enable + start the service on first install (config ships enabled=1
# already; the explicit set tolerates a modified /etc/config).
uci -q get wellboard.main >/dev/null || {
	uci set wellboard.main=wellboard
	uci set wellboard.main.enabled='1'
	uci set wellboard.main.port='8090'
	uci set wellboard.main.state_dir='/etc/wellboard'
	uci set wellboard.main.templates_dir='/usr/share/wellboard/templates'
	uci commit wellboard
}

# Enable autostart symlink (/etc/rc.d/*). Skipped inside a chroot
# install (IPKG_INSTROOT set) where init scripts were not installed.
[ -n "$IPKG_INSTROOT" ] || {
	/etc/init.d/wellboard enable 2>/dev/null || true
}

exit 0
