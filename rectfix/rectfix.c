/* libyt_rectfix: LD_PRELOAD shim for tplayerdemo.
 *
 * tplayerdemo calls TPlayerSetDisplayRect(player, x, y, video_w, video_h) when
 * the decoder reports the video size; the disp layer then shows that rect
 * stretched/misplaced on the 1024x768 panel (and the demo's stdin
 * "set dst_rect" parser mangles values, so it can't be fixed from outside).
 * This shim rewrites every call into an aspect-fitted, centered rect. The w/h
 * the demo passes ARE the decoded video dimensions, so the letterbox math has
 * exactly what it needs.
 *
 * Build: aarch64-linux-gnu-gcc -shared -fPIC -O2 rectfix.c -o libyt_rectfix.so -ldl
 * Use:   LD_PRELOAD=/mnt/SDCARD/Videos/libyt_rectfix.so tplayerdemo <url>
 */
#define _GNU_SOURCE
#include <dlfcn.h>
#include <stdio.h>

#define PANEL_W 1024
#define PANEL_H 768

__attribute__((constructor)) static void rectfix_init(void)
{
    fprintf(stderr, "[rectfix] loaded\n");
}

int TPlayerSetDisplayRect(void *player, int x, int y, unsigned int w, unsigned int h)
{
    static int (*real)(void *, int, int, unsigned int, unsigned int) = 0;
    if (!real)
        real = (int (*)(void *, int, int, unsigned int, unsigned int))
            dlsym(RTLD_NEXT, "TPlayerSetDisplayRect");
    if (!real) {
        fprintf(stderr, "[rectfix] dlsym failed: %s\n", dlerror());
        return -1;
    }

    if (w > 0 && h > 0) {
        unsigned int dw = PANEL_W;
        unsigned int dh = (unsigned int)((unsigned long)PANEL_W * h / w);
        if (dh > PANEL_H) {
            dh = PANEL_H;
            dw = (unsigned int)((unsigned long)PANEL_H * w / h);
        }
        dw &= ~1u;
        dh &= ~1u;
        int dx = (PANEL_W - (int)dw) / 2;
        int dy = (PANEL_H - (int)dh) / 2;
        fprintf(stderr, "[rectfix] %dx%d -> %d,%d %ux%u\n", w, h, dx, dy, dw, dh);
        return real(player, dx, dy, dw, dh);
    }
    return real(player, x, y, w, h);
}
