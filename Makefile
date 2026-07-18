BINARY      := logitux
PREFIX      := $(HOME)/.local
BINDIR      := $(PREFIX)/bin
DESKTOP_DIR := $(HOME)/.local/share/applications
UDEV_RULE   := /etc/udev/rules.d/99-logitux.rules

.PHONY: build test install uninstall run clean

build:
	go build -o bin/$(BINARY) ./cmd/logitux

test:
	go vet ./...
	go test ./...

install: build
	mkdir -p $(BINDIR)
	install -m 0755 bin/$(BINARY) $(BINDIR)/$(BINARY)
	mkdir -p $(DESKTOP_DIR)
	install -m 0644 install/logitux.desktop $(DESKTOP_DIR)/logitux.desktop
	sudo install -m 0644 install/99-logitux.rules $(UDEV_RULE)
	sudo udevadm control --reload-rules
	sudo udevadm trigger
	@echo ""
	@echo "Installed to $(BINDIR)/$(BINARY)."
	@echo "Unplug and replug your Logitech device so the new udev rule applies."

uninstall:
	rm -f $(BINDIR)/$(BINARY)
	rm -f $(DESKTOP_DIR)/logitux.desktop
	sudo rm -f $(UDEV_RULE)
	sudo udevadm control --reload-rules

run: build
	./bin/$(BINARY)

clean:
	rm -rf bin
