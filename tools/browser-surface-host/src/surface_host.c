#include <X11/Xatom.h>
#include <X11/Xlib.h>
#include <errno.h>
#include <limits.h>
#include <signal.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/select.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>

#define MARKER_ATOM "_SPARKCLAW_SURFACE_MARKER"
#define ROLE_ATOM "_SPARKCLAW_SURFACE_ROLE"
#define FRAME_ROLE_ATOM "_SPARKCLAW_SURFACE_FRAME_ROLE"
#define MIN_MARKER_LENGTH 32U
#define MAX_MARKER_LENGTH 128U
#define MAX_DIMENSION 16384

typedef struct {
    Window anchor;
    Window child;
    pid_t expected_anchor_pid;
    const char *expected_anchor_exe;
    const char *anchor_marker;
    pid_t expected_pid;
    const char *expected_exe;
    const char *marker;
    const char *role;
    int x;
    int y;
    unsigned int width;
    unsigned int height;
} Options;

typedef struct {
    Window parent;
    int parent_x;
    int parent_y;
    int root_x;
    int root_y;
    unsigned int width;
    unsigned int height;
    bool mapped;
} OriginalWindow;

typedef struct {
    int frame_x;
    int frame_y;
    int child_x;
    int child_y;
    unsigned int width;
    unsigned int height;
} SurfaceGeometry;

static volatile sig_atomic_t stopping = 0;
static volatile sig_atomic_t x_error = 0;

static void stop_handler(int signal_number) {
    (void)signal_number;
    stopping = 1;
}

static int error_handler(Display *display, XErrorEvent *event) {
    (void)display;
    (void)event;
    x_error = 1;
    return 0;
}

static void usage(const char *program) {
    fprintf(stderr,
            "usage: %s --anchor XID --expected-anchor-pid PID "
            "--expected-anchor-exe PATH --anchor-marker TOKEN "
            "--child XID --expected-pid PID "
            "--expected-exe PATH --marker TOKEN --role personal|task "
            "--x PX --y PX --width PX --height PX\n",
            program);
}

static bool parse_ulong(const char *raw, unsigned long *value) {
    char *end = NULL;
    errno = 0;
    unsigned long parsed = strtoul(raw, &end, 0);
    if (errno != 0 || end == raw || *end != '\0') return false;
    *value = parsed;
    return true;
}

static bool parse_int(const char *raw, int *value) {
    char *end = NULL;
    errno = 0;
    long parsed = strtol(raw, &end, 10);
    if (errno != 0 || end == raw || *end != '\0' || parsed < INT_MIN || parsed > INT_MAX) return false;
    *value = (int)parsed;
    return true;
}

static bool parse_positive_uint(const char *raw, unsigned int *value) {
    unsigned long parsed = 0;
    if (!parse_ulong(raw, &parsed) || parsed == 0 || parsed > MAX_DIMENSION) return false;
    *value = (unsigned int)parsed;
    return true;
}

static bool parse_options(int argc, char **argv, Options *options) {
    memset(options, 0, sizeof(*options));
    for (int index = 1; index < argc; index += 2) {
        if (index + 1 >= argc) return false;
        const char *name = argv[index];
        const char *value = argv[index + 1];
        unsigned long parsed = 0;
        int signed_value = 0;
        if (strcmp(name, "--anchor") == 0 && parse_ulong(value, &parsed)) {
            options->anchor = (Window)parsed;
        } else if (strcmp(name, "--expected-anchor-pid") == 0 &&
                   parse_ulong(value, &parsed) && parsed <= INT_MAX) {
            options->expected_anchor_pid = (pid_t)parsed;
        } else if (strcmp(name, "--expected-anchor-exe") == 0) {
            options->expected_anchor_exe = value;
        } else if (strcmp(name, "--anchor-marker") == 0) {
            options->anchor_marker = value;
        } else if (strcmp(name, "--child") == 0 && parse_ulong(value, &parsed)) {
            options->child = (Window)parsed;
        } else if (strcmp(name, "--expected-pid") == 0 && parse_ulong(value, &parsed) && parsed <= INT_MAX) {
            options->expected_pid = (pid_t)parsed;
        } else if (strcmp(name, "--expected-exe") == 0) {
            options->expected_exe = value;
        } else if (strcmp(name, "--marker") == 0) {
            options->marker = value;
        } else if (strcmp(name, "--role") == 0) {
            options->role = value;
        } else if (strcmp(name, "--x") == 0 && parse_int(value, &signed_value)) {
            options->x = signed_value;
        } else if (strcmp(name, "--y") == 0 && parse_int(value, &signed_value)) {
            options->y = signed_value;
        } else if (strcmp(name, "--width") == 0 && parse_positive_uint(value, &options->width)) {
        } else if (strcmp(name, "--height") == 0 && parse_positive_uint(value, &options->height)) {
        } else {
            return false;
        }
    }
    size_t marker_length = options->marker == NULL ? 0 : strlen(options->marker);
    size_t anchor_marker_length = options->anchor_marker == NULL ? 0 : strlen(options->anchor_marker);
    return options->anchor != None && options->child != None && options->anchor != options->child &&
           options->expected_anchor_pid > 0 && options->expected_anchor_exe != NULL &&
           options->expected_anchor_exe[0] == '/' &&
           anchor_marker_length >= MIN_MARKER_LENGTH && anchor_marker_length <= MAX_MARKER_LENGTH &&
           options->expected_pid > 0 && options->expected_exe != NULL && options->expected_exe[0] == '/' &&
           marker_length >= MIN_MARKER_LENGTH && marker_length <= MAX_MARKER_LENGTH &&
           options->role != NULL &&
           (strcmp(options->role, "personal") == 0 || strcmp(options->role, "task") == 0) &&
           options->width > 0 && options->height > 0;
}

static bool window_attributes(Display *display, Window window, XWindowAttributes *attributes) {
    x_error = 0;
    Status result = XGetWindowAttributes(display, window, attributes);
    XSync(display, False);
    return result != 0 && x_error == 0;
}

static bool window_parent(Display *display, Window window, Window *parent) {
    Window root = None;
    Window found_parent = None;
    Window *children = NULL;
    unsigned int count = 0;
    x_error = 0;
    Status result = XQueryTree(display, window, &root, &found_parent, &children, &count);
    if (children != NULL) XFree(children);
    XSync(display, False);
    if (result == 0 || x_error != 0) return false;
    *parent = found_parent;
    return true;
}

static bool is_window_or_descendant(Display *display, Window window, Window ancestor) {
    Window current = window;
    for (unsigned int depth = 0; depth < 128U && current != None && current != PointerRoot; depth++) {
        if (current == ancestor) return true;
        Window parent = None;
        if (!window_parent(display, current, &parent) || parent == current) return false;
        current = parent;
    }
    return false;
}

static bool root_child_for_window(Display *display, Window window, Window *root_child) {
    Window root = DefaultRootWindow(display);
    Window current = window;
    for (unsigned int depth = 0; depth < 128U && current != None; depth++) {
        Window parent = None;
        if (!window_parent(display, current, &parent) || parent == current) return false;
        if (parent == root) {
            *root_child = current;
            return true;
        }
        current = parent;
    }
    return false;
}

static bool move_focus_out_of_task(Display *display, Window anchor, Window child) {
    Window focused = None;
    int revert_to = 0;
    x_error = 0;
    XGetInputFocus(display, &focused, &revert_to);
    XSync(display, False);
    if (x_error != 0) return false;
    if (!is_window_or_descendant(display, focused, child)) return true;

    XWindowAttributes attributes;
    Window target = window_attributes(display, anchor, &attributes) && attributes.map_state == IsViewable
                        ? anchor
                        : PointerRoot;
    x_error = 0;
    XSetInputFocus(display, target, RevertToParent, CurrentTime);
    XSync(display, False);
    return x_error == 0;
}

static bool string_property(Display *display, Window window, const char *name, char *buffer, size_t size) {
    Atom property = XInternAtom(display, name, False);
    Atom actual_type = None;
    int actual_format = 0;
    unsigned long count = 0;
    unsigned long remaining = 0;
    unsigned char *data = NULL;
    x_error = 0;
    int result = XGetWindowProperty(display, window, property, 0, (long)((size + 3U) / 4U), False,
                                    AnyPropertyType, &actual_type, &actual_format, &count, &remaining, &data);
    XSync(display, False);
    if (result != Success || x_error != 0 || data == NULL || actual_format != 8 || count == 0 ||
        count >= size || remaining != 0) {
        if (data != NULL) XFree(data);
        return false;
    }
    memcpy(buffer, data, count);
    buffer[count] = '\0';
    XFree(data);
    return actual_type == XA_STRING;
}

static bool cardinal_property(Display *display, Window window, const char *name, unsigned long *value) {
    Atom property = XInternAtom(display, name, False);
    Atom actual_type = None;
    int actual_format = 0;
    unsigned long count = 0;
    unsigned long remaining = 0;
    unsigned char *data = NULL;
    x_error = 0;
    int result = XGetWindowProperty(display, window, property, 0, 1, False, XA_CARDINAL,
                                    &actual_type, &actual_format, &count, &remaining, &data);
    XSync(display, False);
    if (result != Success || x_error != 0 || data == NULL || actual_type != XA_CARDINAL ||
        actual_format != 32 || count != 1 || remaining != 0) {
        if (data != NULL) XFree(data);
        return false;
    }
    *value = *((unsigned long *)data);
    XFree(data);
    return true;
}

static bool same_executable(pid_t pid, const char *expected) {
    char proc_path[64];
    char actual[PATH_MAX];
    char expected_real[PATH_MAX];
    snprintf(proc_path, sizeof(proc_path), "/proc/%ld/exe", (long)pid);
    ssize_t length = readlink(proc_path, actual, sizeof(actual) - 1U);
    if (length <= 0 || (size_t)length >= sizeof(actual)) return false;
    actual[length] = '\0';
    if (realpath(expected, expected_real) == NULL) return false;
    return strcmp(actual, expected_real) == 0;
}

static bool same_owner(pid_t pid) {
    char proc_path[64];
    struct stat status;
    snprintf(proc_path, sizeof(proc_path), "/proc/%ld", (long)pid);
    return stat(proc_path, &status) == 0 && status.st_uid == getuid();
}

static bool exact_identity(Display *display, Window window, pid_t expected_pid,
                           const char *expected_exe, const char *expected_marker,
                           const char *expected_role) {
    unsigned long window_pid = 0;
    char marker[MAX_MARKER_LENGTH + 1U];
    char role[16];
    return cardinal_property(display, window, "_NET_WM_PID", &window_pid) &&
           window_pid == (unsigned long)expected_pid && same_owner(expected_pid) &&
           same_executable(expected_pid, expected_exe) &&
           string_property(display, window, MARKER_ATOM, marker, sizeof(marker)) &&
           strcmp(marker, expected_marker) == 0 &&
           string_property(display, window, ROLE_ATOM, role, sizeof(role)) &&
           strcmp(role, expected_role) == 0;
}

static bool root_position(Display *display, Window window, int *x, int *y) {
    Window child = None;
    return XTranslateCoordinates(display, window, DefaultRootWindow(display), 0, 0, x, y, &child) != 0;
}

static bool capture_original(Display *display, Window child, OriginalWindow *original) {
    XWindowAttributes attributes;
    if (!window_attributes(display, child, &attributes) || !window_parent(display, child, &original->parent) ||
        !root_position(display, child, &original->root_x, &original->root_y)) return false;
    original->width = (unsigned int)attributes.width;
    original->height = (unsigned int)attributes.height;
    original->parent_x = attributes.x;
    original->parent_y = attributes.y;
    original->mapped = attributes.map_state != IsUnmapped;
    return true;
}

static void set_string_property(Display *display, Window window, const char *name, const char *value) {
    Atom property = XInternAtom(display, name, False);
    XChangeProperty(display, window, property, XA_STRING, 8, PropModeReplace,
                    (const unsigned char *)value, (int)strlen(value));
}

static bool clipped_geometry(Display *display, Window anchor, const Options *options,
                             SurfaceGeometry *geometry, bool *visible) {
    XWindowAttributes attributes;
    if (!window_attributes(display, anchor, &attributes)) return false;
    if (attributes.map_state != IsViewable) {
        *visible = false;
        return true;
    }

    int anchor_x = 0;
    int anchor_y = 0;
    if (!root_position(display, anchor, &anchor_x, &anchor_y)) return false;

    int64_t requested_left = options->x;
    int64_t requested_top = options->y;
    int64_t requested_right = requested_left + (int64_t)options->width;
    int64_t requested_bottom = requested_top + (int64_t)options->height;
    int64_t clipped_left = requested_left < 0 ? 0 : requested_left;
    int64_t clipped_top = requested_top < 0 ? 0 : requested_top;
    int64_t clipped_right = requested_right > attributes.width ? attributes.width : requested_right;
    int64_t clipped_bottom = requested_bottom > attributes.height ? attributes.height : requested_bottom;
    if (clipped_left >= clipped_right || clipped_top >= clipped_bottom) {
        *visible = false;
        return true;
    }

    int64_t frame_x = (int64_t)anchor_x + clipped_left;
    int64_t frame_y = (int64_t)anchor_y + clipped_top;
    if (frame_x < INT_MIN || frame_x > INT_MAX || frame_y < INT_MIN || frame_y > INT_MAX) return false;
    geometry->frame_x = (int)frame_x;
    geometry->frame_y = (int)frame_y;
    geometry->child_x = (int)(requested_left - clipped_left);
    geometry->child_y = (int)(requested_top - clipped_top);
    geometry->width = (unsigned int)(clipped_right - clipped_left);
    geometry->height = (unsigned int)(clipped_bottom - clipped_top);
    *visible = true;
    return true;
}

static bool same_geometry(const SurfaceGeometry *left, const SurfaceGeometry *right) {
    return left->frame_x == right->frame_x && left->frame_y == right->frame_y &&
           left->child_x == right->child_x && left->child_y == right->child_y &&
           left->width == right->width && left->height == right->height;
}

static bool sync_frame(Display *display, Window anchor, Window frame, Window child, Window shield,
                       const Options *options, bool *frame_mapped,
                       SurfaceGeometry *last_geometry, bool *have_geometry) {
    SurfaceGeometry geometry;
    bool visible = false;
    if (!clipped_geometry(display, anchor, options, &geometry, &visible)) return false;
    if (!visible) {
        if (*frame_mapped) {
            XUnmapWindow(display, frame);
            *frame_mapped = false;
        }
        return true;
    }

    if (!*have_geometry || !same_geometry(&geometry, last_geometry)) {
        XMoveResizeWindow(display, frame, geometry.frame_x, geometry.frame_y,
                          geometry.width, geometry.height);
        XMoveResizeWindow(display, child, geometry.child_x, geometry.child_y,
                          options->width, options->height);
        if (shield != None) {
            XMoveResizeWindow(display, shield, 0, 0, geometry.width, geometry.height);
        }
        *last_geometry = geometry;
        *have_geometry = true;
    }
    Window anchor_root_child = None;
    if (!root_child_for_window(display, anchor, &anchor_root_child)) return false;
    XWindowChanges stacking;
    memset(&stacking, 0, sizeof(stacking));
    stacking.sibling = anchor_root_child;
    stacking.stack_mode = Above;
    XConfigureWindow(display, frame, CWSibling | CWStackMode, &stacking);
    if (!*frame_mapped) {
        XMapWindow(display, frame);
        *frame_mapped = true;
    }
    return true;
}

static void restore_child(Display *display, Window child, const OriginalWindow *original) {
    XWindowAttributes attributes;
    Window target_parent = window_attributes(display, original->parent, &attributes)
                               ? original->parent
                               : DefaultRootWindow(display);
    int target_x = target_parent == DefaultRootWindow(display) ? original->root_x : original->parent_x;
    int target_y = target_parent == DefaultRootWindow(display) ? original->root_y : original->parent_y;
    XUnmapWindow(display, child);
    XReparentWindow(display, child, target_parent, target_x, target_y);
    XResizeWindow(display, child, original->width, original->height);
    XRemoveFromSaveSet(display, child);
    if (original->mapped) XMapWindow(display, child);
    XSync(display, False);
}

int main(int argc, char **argv) {
    Options options;
    if (!parse_options(argc, argv, &options)) {
        usage(argv[0]);
        return 2;
    }
    signal(SIGINT, stop_handler);
    signal(SIGTERM, stop_handler);
    XSetErrorHandler(error_handler);
    Display *display = XOpenDisplay(NULL);
    if (display == NULL) {
        fprintf(stderr, "surface-host: X11 display is unavailable\n");
        return 3;
    }
    XWindowAttributes anchor_attributes;
    XWindowAttributes child_attributes;
    if (!window_attributes(display, options.anchor, &anchor_attributes) ||
        !window_attributes(display, options.child, &child_attributes) ||
        options.anchor == DefaultRootWindow(display) || options.child == DefaultRootWindow(display) ||
        !exact_identity(display, options.anchor, options.expected_anchor_pid,
                        options.expected_anchor_exe, options.anchor_marker, "anchor") ||
        !exact_identity(display, options.child, options.expected_pid,
                        options.expected_exe, options.marker, options.role)) {
        fprintf(stderr, "surface-host: window identity rejected\n");
        XCloseDisplay(display);
        return 4;
    }
    OriginalWindow original;
    if (!capture_original(display, options.child, &original)) {
        fprintf(stderr, "surface-host: original window state is unavailable\n");
        XCloseDisplay(display);
        return 5;
    }

    Window root = DefaultRootWindow(display);
    XSetWindowAttributes frame_attributes;
    memset(&frame_attributes, 0, sizeof(frame_attributes));
    frame_attributes.override_redirect = True;
    frame_attributes.event_mask = StructureNotifyMask | ExposureMask;
    Window frame = XCreateWindow(display, root, 0, 0, options.width, options.height, 0,
                                 CopyFromParent, InputOutput, CopyFromParent,
                                 CWOverrideRedirect | CWEventMask, &frame_attributes);
    set_string_property(display, frame, FRAME_ROLE_ATOM, options.role);
    XSelectInput(display, options.anchor, StructureNotifyMask);
    XSelectInput(display, options.child, StructureNotifyMask | FocusChangeMask);
    SurfaceGeometry initial_geometry;
    bool initially_visible = false;
    if (!clipped_geometry(display, options.anchor, &options,
                          &initial_geometry, &initially_visible)) {
        fprintf(stderr, "surface-host: anchor position is unavailable\n");
        XDestroyWindow(display, frame);
        XCloseDisplay(display);
        return 6;
    }

    x_error = 0;
    XAddToSaveSet(display, options.child);
    XUnmapWindow(display, options.child);
    XReparentWindow(display, options.child, frame, 0, 0);
    XMoveResizeWindow(display, options.child, 0, 0, options.width, options.height);
    XMapWindow(display, options.child);
    Window shield = None;
    if (strcmp(options.role, "task") == 0) {
        XSetWindowAttributes shield_attributes;
        memset(&shield_attributes, 0, sizeof(shield_attributes));
        shield_attributes.override_redirect = True;
        shield_attributes.event_mask = ButtonPressMask | ButtonReleaseMask | PointerMotionMask |
                                       KeyPressMask | KeyReleaseMask;
        shield = XCreateWindow(display, frame, 0, 0, options.width, options.height, 0, 0,
                               InputOnly, CopyFromParent, CWOverrideRedirect | CWEventMask,
                               &shield_attributes);
        XMapRaised(display, shield);
        if (!move_focus_out_of_task(display, options.anchor, options.child)) x_error = 1;
    }
    bool frame_mapped = false;
    SurfaceGeometry last_geometry;
    memset(&last_geometry, 0, sizeof(last_geometry));
    bool have_geometry = false;
    if (!sync_frame(display, options.anchor, frame, options.child, shield, &options,
                    &frame_mapped, &last_geometry, &have_geometry)) x_error = 1;
    XSync(display, False);
    if (x_error != 0) {
        fprintf(stderr, "surface-host: X11 attach failed\n");
        restore_child(display, options.child, &original);
        XDestroyWindow(display, frame);
        XCloseDisplay(display);
        return 7;
    }

    printf("{\"state\":\"attached\",\"frame\":\"0x%lx\",\"shield\":\"0x%lx\","
           "\"child\":\"0x%lx\",\"role\":\"%s\"}\n",
           frame, shield, options.child, options.role);
    fflush(stdout);

    int connection = ConnectionNumber(display);
    bool anchor_alive = true;
    bool child_alive = true;
    while (!stopping && anchor_alive && child_alive) {
        fd_set reads;
        FD_ZERO(&reads);
        FD_SET(connection, &reads);
        struct timeval timeout = {.tv_sec = 0, .tv_usec = 16000};
        int selected = select(connection + 1, &reads, NULL, NULL, &timeout);
        if (selected < 0 && errno != EINTR) break;
        while (XPending(display) > 0) {
            XEvent event;
            XNextEvent(display, &event);
            if (event.xany.window == options.child && event.type == DestroyNotify) {
                child_alive = false;
            } else if (event.xany.window == options.child && event.type == FocusIn &&
                       strcmp(options.role, "task") == 0) {
                if (!move_focus_out_of_task(display, options.anchor, options.child)) {
                    XUnmapWindow(display, frame);
                    frame_mapped = false;
                    stopping = 1;
                }
            } else if (event.xany.window != options.anchor) {
                continue;
            } else if (event.type == DestroyNotify) {
                anchor_alive = false;
            } else if (event.type == UnmapNotify) {
                XUnmapWindow(display, frame);
                frame_mapped = false;
            } else if (event.type == MapNotify) {
                (void)sync_frame(display, options.anchor, frame, options.child, shield, &options,
                                 &frame_mapped, &last_geometry, &have_geometry);
            } else if (event.type == ConfigureNotify) {
                (void)sync_frame(display, options.anchor, frame, options.child, shield, &options,
                                 &frame_mapped, &last_geometry, &have_geometry);
            }
        }
        if (anchor_alive && child_alive &&
            !sync_frame(display, options.anchor, frame, options.child, shield, &options,
                        &frame_mapped, &last_geometry, &have_geometry)) {
            anchor_alive = false;
        }
        XFlush(display);
    }

    if (child_alive) restore_child(display, options.child, &original);
    XDestroyWindow(display, frame);
    XCloseDisplay(display);
    return 0;
}
