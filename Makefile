BUILD_DIR := build/linux-amd64

.PHONY: build dev install install-user uninstall-user pkg clean

build:
	go tool mygo build -o build/

dev:
	go tool mygo dev

# Install for current user via mygo's install.sh (no root needed)
install-user: build
	sh $(BUILD_DIR)/install.sh $(BUILD_DIR)/http-gui-*-linux-amd64.tar.gz

uninstall-user:
	sh $(BUILD_DIR)/install.sh --uninstall

# Arch Linux: build and install via PKGBUILD
pkg: build
	cd $(BUILD_DIR) && makepkg -si

# System-wide install (needs root)
install: build
	install -Dm755 $(BUILD_DIR)/http-gui $(DESTDIR)/usr/local/bin/http-gui
	install -Dm644 resources/http_gui.desktop $(DESTDIR)/usr/share/applications/http-gui.desktop
	install -Dm644 resources/icon-48.png $(DESTDIR)/usr/share/icons/hicolor/48x48/apps/http-gui.png
	install -Dm644 resources/icon-128.png $(DESTDIR)/usr/share/icons/hicolor/128x128/apps/http-gui.png
	install -Dm644 resources/icon.png $(DESTDIR)/usr/share/icons/hicolor/256x256/apps/http-gui.png
	install -Dm644 resources/icon-512.png $(DESTDIR)/usr/share/icons/hicolor/512x512/apps/http-gui.png
	gtk-update-icon-cache -f -t $(DESTDIR)/usr/share/icons/hicolor 2>/dev/null || true
	update-desktop-database $(DESTDIR)/usr/share/applications 2>/dev/null || true

clean:
	rm -rf build/

