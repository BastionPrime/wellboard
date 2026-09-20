include $(TOPDIR)/rules.mk

# Minimal LuCI app for WellBoard: a Services-menu entry that opens the
# WellBoard web UI. The UI is a full SPA served by the wellboard binary
# itself, so LuCI only needs a menu node with an external redirect —
# no views, no rpcd ACLs beyond the menu itself.

PKG_NAME:=luci-app-wellboard
PKG_VERSION:=0.1.0
PKG_RELEASE:=1
LUCI_TITLE:=LuCI support for WellBoard
LUCI_DEPENDS:=+wellboard +luci-base

LUCI_PKGARCH:=all

include $(TOPDIR)/feeds/luci/luci.mk

# call BuildPackage - OpenWrt buildroot signature
