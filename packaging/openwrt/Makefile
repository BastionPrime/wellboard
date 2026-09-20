include $(TOPDIR)/rules.mk

# WellBoard is NOT compiled inside the buildroot: the binary is
# prebuilt in a golang:1.23-alpine docker container by the Makefile in
# the repository root (targets: make cross / scripts/cross-compile.sh)
# and dropped into this directory before packaging. Build/Compile is
# therefore a no-op that only verifies the expected binary exists.
PREBUILT_DIR ?= $(CURDIR)/prebuilt

PKG_NAME:=wellboard
PKG_VERSION:=0.1.0
PKG_RELEASE:=1
PKG_LICENSE:=AGPL-3.0-only
PKG_MAINTAINER:=WellBoard project <dev@wellboard.local>

include $(INCLUDE_DIR)/package.mk

define Package/wellboard
  SECTION:=net
  CATEGORY:=Network
  SUBMENU:=Proxy Utilities
  TITLE:=Remnawave subscription manager for nikki/mihomo
  URL:=https://wellboard.local
  DEPENDS:=+ca-bundle +curl +nikki
endef

define Package/wellboard/description
  WellBoard manages a Remnawave subscription (servers, quotas, HWID),
  builds routing templates and generates the nikki/mihomo profile,
  then applies it with health checks and automatic rollback.
  Serves a web UI on port 8090.
endef

define Package/wellboard/conffiles
/etc/config/wellboard
endef

define Build/Prepare
	mkdir -p $(PREBUILT_DIR)
	[ -f $(PREBUILT_DIR)/wellboard ] || { \
		echo "wellboard: prebuilt binary not found: $(PREBUILT_DIR)/wellboard" >&2; \
		echo "build it with: make cross (repo root Makefile)" >&2; \
		exit 1; \
	}
endef

define Build/Compile
	# nothing to compile: the binary is prebuilt (see Build/Prepare)
endef

define Package/wellboard/install
	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) $(PREBUILT_DIR)/wellboard $(1)/usr/bin/wellboard

	$(INSTALL_DIR) $(1)/usr/share/wellboard
	$(CP) $(CURDIR)/templates $(1)/usr/share/wellboard/templates
	find $(1)/usr/share/wellboard/templates -type d | xargs chmod 0755
	find $(1)/usr/share/wellboard/templates -type f | xargs chmod 0644

	$(INSTALL_DIR) $(1)/etc/wellboard
	chmod 0700 $(1)/etc/wellboard

	$(INSTALL_DIR) $(1)/etc/config
	$(INSTALL_CONF) $(CURDIR)/files/wellboard.config $(1)/etc/config/wellboard

	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) $(CURDIR)/files/wellboard.init $(1)/etc/init.d/wellboard

	$(INSTALL_DIR) $(1)/etc/uci-defaults
	$(INSTALL_BIN) $(CURDIR)/files/uci-defaults.sh $(1)/etc/uci-defaults/99_wellboard

	$(INSTALL_DIR) $(1)/lib/upgrade/keep.d
	$(INSTALL_DATA) $(CURDIR)/files/keep.d/wellboard $(1)/lib/upgrade/keep.d/wellboard
endef

$(eval $(call BuildPackage,wellboard))
