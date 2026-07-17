/* libyt_rectfix: LD_PRELOAD shim for tplayerdemo — aspect-correct letterbox.
 *
 * History of approaches:
 *  - stdin "set dst_rect": the demo's parser mangles values (small top-left).
 *  - Interposing TPlayerSetDisplayRect: the demo never calls it during URL
 *    playback ("[rectfix] loaded" appeared; rewrite never fired) — the rect is
 *    set internally by awplayer's LayerSetDisplayRect.
 *  - THIS: interpose ioctl() at the /dev/disp boundary. Every layer config the
 *    player submits passes DISP_LAYER_SET_CONFIG2 (0x49, per-frame); we rewrite
 *    screen_win for enabled channel-0 (video/scaler) layers to an aspect-fitted
 *    centered rect computed from the buffer crop. UI runs on channel 1 —
 *    untouched. Structs verbatim from Allwinner BSP 4.9 sunxi_display2.h.
 *
 * Build: aarch64-linux-gnu-gcc -shared -fPIC -O2 rectfix.c -o libyt_rectfix.so -ldl
 * Use:   LD_PRELOAD=/mnt/SDCARD/Videos/libyt_rectfix.so tplayerdemo <url>
 */
#define _GNU_SOURCE
#include <dlfcn.h>
#include <stdarg.h>
#include <stdbool.h>
#include <stdio.h>

#define PANEL_W 1024
#define PANEL_H 768

#define DISP_LAYER_SET_CONFIG  0x47
#define DISP_LAYER_SET_CONFIG2 0x49

struct disp_rect   { int x, y; unsigned int width, height; };
struct disp_rect64 { long long x, y, width, height; };
struct disp_rectsz { unsigned int width, height; };

struct disp_fb_info2 {
    int fd;
    struct disp_rectsz size[3];
    unsigned int align[3];
    int format;       /* enum disp_pixel_format */
    int color_space;  /* enum disp_color_space */
    int trd_right_fd;
    bool pre_multiply;
    struct disp_rect64 crop;
    int flags;        /* enum disp_buffer_flags */
    int scan;         /* enum disp_scan_flags */
    int eotf;         /* enum disp_eotf */
    int depth;
    unsigned int fbd_en;
    int metadata_fd;
    unsigned int metadata_size;
    unsigned int metadata_flag;
};

struct disp_atw_info { int used; int mode; unsigned int b_row, b_col, cols; };

struct disp_layer_info2 {
    int mode; /* enum disp_layer_mode */
    unsigned char zorder, alpha_mode, alpha_value;
    struct disp_rect screen_win;
    bool b_trd_out;
    int out_trd_mode; /* enum disp_3d_out_mode */
    union {
        unsigned int color;
        struct disp_fb_info2 fb;
    };
    unsigned int id;
    struct disp_atw_info atw;
};

struct disp_layer_config2 {
    struct disp_layer_info2 info;
    bool enable;
    unsigned int channel, layer_id;
};

__attribute__((constructor)) static void rectfix_init(void)
{
    fprintf(stderr, "[rectfix] loaded (ioctl mode)\n");
}

static void fit_rect(long long vw, long long vh, struct disp_rect *win)
{
    unsigned int dw = PANEL_W;
    unsigned int dh = (unsigned int)((long long)PANEL_W * vh / vw);
    if (dh > PANEL_H) {
        dh = PANEL_H;
        dw = (unsigned int)((long long)PANEL_H * vw / vh);
    }
    dw &= ~1u;
    dh &= ~1u;
    win->x = (PANEL_W - (int)dw) / 2;
    win->y = (PANEL_H - (int)dh) / 2;
    win->width = dw;
    win->height = dh;
}

int ioctl(int fd, unsigned long request, ...)
{
    va_list ap;
    va_start(ap, request);
    void *arg = va_arg(ap, void *);
    va_end(ap);

    static int (*real)(int, unsigned long, ...) = 0;
    if (!real)
        real = (int (*)(int, unsigned long, ...))dlsym(RTLD_NEXT, "ioctl");

    if (request == DISP_LAYER_SET_CONFIG2 && arg != 0) {
        unsigned long *ub = (unsigned long *)arg;
        struct disp_layer_config2 *cfgs = (struct disp_layer_config2 *)ub[1];
        unsigned long count = ub[2];
        static int logged = 0;
        for (unsigned long i = 0; cfgs != 0 && i < count && i < 16; i++) {
            struct disp_layer_config2 *c = &cfgs[i];
            if (!c->enable || c->channel != 0)
                continue; /* video/scaler lives on ch0; UI fb is ch1 */
            long long vw = c->info.fb.crop.width >> 32;
            long long vh = c->info.fb.crop.height >> 32;
            if (vw <= 0 || vh <= 0) {
                vw = c->info.fb.size[0].width;
                vh = c->info.fb.size[0].height;
            }
            if (vw <= 0 || vh <= 0)
                continue;
            struct disp_rect old = c->info.screen_win;
            fit_rect(vw, vh, &c->info.screen_win);
            if (logged < 3) {
                logged++;
                fprintf(stderr,
                    "[rectfix] ch0 video %lldx%lld win (%d,%d %ux%u) -> (%d,%d %ux%u)\n",
                    vw, vh, old.x, old.y, old.width, old.height,
                    c->info.screen_win.x, c->info.screen_win.y,
                    c->info.screen_win.width, c->info.screen_win.height);
            }
        }
    }

    return real(fd, request, arg);
}
