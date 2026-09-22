CC ?= cc
CFLAGS ?= -O2 -g
CPPFLAGS ?=
LDFLAGS ?=

CPPFLAGS += -D_POSIX_C_SOURCE=200809L
CFLAGS += -std=c11 -Wall -Wextra -Werror -pedantic

.PHONY: all clean test test-electron-anchor

all: bin/surface-host bin/surface-fixture

bin:
	mkdir -p bin

bin/surface-host: src/surface_host.c | bin
	$(CC) $(CPPFLAGS) $(CFLAGS) $< -o $@ $(LDFLAGS) -lX11

bin/surface-fixture: test/surface_fixture.c | bin
	$(CC) $(CPPFLAGS) $(CFLAGS) $< -o $@ $(LDFLAGS) -lX11

test: all
	python3 test/run_electron_anchor_qualification.py --suite synthetic

test-electron-anchor: all
	node -e "require('./test/electron-anchor/node_modules/electron')"
	python3 test/run_electron_anchor_qualification.py \
		--suite electron-anchor \
		--electron test/electron-anchor/node_modules/electron/dist/electron

clean:
	rm -f bin/surface-host bin/surface-fixture
