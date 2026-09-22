#include <X11/Xatom.h>
#include <X11/Xlib.h>
#include <errno.h>
#include <signal.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/select.h>
#include <unistd.h>

static volatile sig_atomic_t stopping = 0;

static void stop_handler(int signal_number) {
    (void)signal_number;
    stopping = 1;
}

static void set_string(Display *display, Window window, const char *name, const char *value) {
    Atom atom = XInternAtom(display, name, False);
    XChangeProperty(display, window, atom, XA_STRING, 8, PropModeReplace,
                    (const unsigned char *)value, (int)strlen(value));
}

static void set_pid(Display *display, Window window) {
    Atom atom = XInternAtom(display, "_NET_WM_PID", False);
    unsigned long pid = (unsigned long)getpid();
    XChangeProperty(display, window, atom, XA_CARDINAL, 32, PropModeReplace,
                    (const unsigned char *)&pid, 1);
}

int main(int argc, char **argv) {
    const char *role = "anchor";
    const char *marker = "0123456789abcdef0123456789abcdef";
    int x = 100;
    int y = 100;
    bool override_redirect = false;
    for (int index = 1; index < argc; index += 2) {
        if (index + 1 >= argc) return 2;
        if (strcmp(argv[index], "--role") == 0) role = argv[index + 1];
        else if (strcmp(argv[index], "--marker") == 0) marker = argv[index + 1];
        else if (strcmp(argv[index], "--x") == 0) x = atoi(argv[index + 1]);
        else if (strcmp(argv[index], "--y") == 0) y = atoi(argv[index + 1]);
        else if (strcmp(argv[index], "--override-redirect") == 0) {
            if (strcmp(argv[index + 1], "true") == 0) override_redirect = true;
            else if (strcmp(argv[index + 1], "false") != 0) return 2;
        }
        else return 2;
    }
    signal(SIGINT, stop_handler);
    signal(SIGTERM, stop_handler);
    Display *display = XOpenDisplay(NULL);
    if (display == NULL) return 3;
    Window root = DefaultRootWindow(display);
    Window window = XCreateSimpleWindow(display, root, x, y, 480, 360, 0,
                                        BlackPixel(display, DefaultScreen(display)),
                                        WhitePixel(display, DefaultScreen(display)));
    XSetWindowAttributes attributes;
    memset(&attributes, 0, sizeof(attributes));
    attributes.override_redirect = override_redirect ? True : False;
    XChangeWindowAttributes(display, window, CWOverrideRedirect, &attributes);
    XStoreName(display, window, role);
    XSelectInput(display, window, StructureNotifyMask | ButtonPressMask | KeyPressMask);
    set_pid(display, window);
    set_string(display, window, "_SPARKCLAW_SURFACE_MARKER", marker);
    set_string(display, window, "_SPARKCLAW_SURFACE_ROLE", role);
    XMapWindow(display, window);
    XFlush(display);
    printf("{\"window\":\"0x%lx\",\"pid\":%ld,\"role\":\"%s\"}\n",
           window, (long)getpid(), role);
    fflush(stdout);

    int connection = ConnectionNumber(display);
    while (!stopping) {
        fd_set reads;
        FD_ZERO(&reads);
        FD_SET(connection, &reads);
        struct timeval timeout = {.tv_sec = 0, .tv_usec = 200000};
        int selected = select(connection + 1, &reads, NULL, NULL, &timeout);
        if (selected < 0 && errno != EINTR) break;
        while (XPending(display) > 0) {
            XEvent event;
            XNextEvent(display, &event);
            if (event.type == ButtonPress) {
                printf("{\"event\":\"button\"}\n");
                fflush(stdout);
            } else if (event.type == KeyPress) {
                printf("{\"event\":\"key\"}\n");
                fflush(stdout);
            }
        }
    }
    XDestroyWindow(display, window);
    XCloseDisplay(display);
    return 0;
}
