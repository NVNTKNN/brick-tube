/* libyt_seekfix: direct-seek shim — capture the TPlayer handle at TPlayerCreate and
 * drive TPlayerSeekTo directly from a control FIFO (/tmp/yt_seek, whole
 * seconds, one per line).
 *
 * Why: the demo's stdin "seekto:" parser MANGLES targets — proven
 * 2026-07-20 (asked 120s/300s -> sought ~playhead on local file, plain
 * http AND the ytproxy chain alike; TPLAYER_NOTIFY_SEEK_COMPLETE fires
 * every time). The seek MECHANISM works; the demo's command layer is the
 * broken part — same class as its "set dst_rect" mangling. So, as with
 * rectfix, interpose below the demo and call the library directly.
 *
 * Build: aarch64-linux-gnu-gcc -shared -fPIC -O2 seekfix.c -o libyt_seekfix.so -ldl -lpthread
 * Use:   LD_PRELOAD=/mnt/SDCARD/Videos/libyt_seekfix.so tplayerdemo <url>
 *        echo 120 > /tmp/yt_seek     # seek to 2:00
 */
#define _GNU_SOURCE
#include <dlfcn.h>
#include <fcntl.h>
#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/stat.h>
#include <unistd.h>

#define SEEK_FIFO "/tmp/yt_seek"

static void *g_player;
static int (*real_seekto)(void *, int);

static void *seek_thread(void *arg)
{
    (void)arg;
    for (;;) {
        int fd = open(SEEK_FIFO, O_RDONLY);  /* blocks until a writer appears */
        if (fd < 0) {
            sleep(1);
            continue;
        }
        char buf[64];
        int n;
        while ((n = (int)read(fd, buf, sizeof buf - 1)) > 0) {
            buf[n] = 0;
            /* parse every complete line in the buffer; act on the LAST valid
             * target so a coalesced repeat burst becomes one seek */
            long sec = -1;
            char *p = buf;
            while (*p) {
                char *end;
                long v = strtol(p, &end, 10);
                if (end == p) {         /* no digits here: skip to next line */
                    while (*end && *end != '\n')
                        end++;
                } else if (*end == '\n' && v >= 0 && v <= 86400) {
                    sec = v;            /* complete, valid line */
                }
                /* an incomplete trailing number (no \n yet) is ignored —
                 * its remainder arrives in the next read */
                while (*end == '\n')
                    end++;
                p = end;
            }
            if (sec < 0)
                continue;
            if (!g_player || !real_seekto) {
                fprintf(stderr, "[seekfix] not ready (player=%p fn=%p)\n",
                        g_player, (void *)real_seekto);
                continue;
            }
            int rc = real_seekto(g_player, (int)sec * 1000);
            fprintf(stderr, "[seekfix] TPlayerSeekTo(%ld s) rc=%d\n", sec, rc);
        }
        close(fd);  /* writer closed; reopen and wait for the next one */
    }
    return 0;
}

void *TPlayerCreate(int type)
{
    static void *(*real_create)(int);
    if (!real_create)
        real_create = (void *(*)(int))dlsym(RTLD_NEXT, "TPlayerCreate");
    void *p = real_create(type);
    g_player = p;
    if (!real_seekto)
        real_seekto = (int (*)(void *, int))dlsym(RTLD_NEXT, "TPlayerSeekTo");
    static int started;
    if (!started) {
        started = 1;
        unlink(SEEK_FIFO);
        mkfifo(SEEK_FIFO, 0666);
        pthread_t t;
        pthread_create(&t, 0, seek_thread, 0);
    }
    fprintf(stderr, "[seekfix] captured player %p, seekto fn %p\n",
            p, (void *)real_seekto);
    return p;
}

int TPlayerDestroy(void *p)
{
    static int (*real_destroy)(void *);
    if (!real_destroy)
        real_destroy = (int (*)(void *))dlsym(RTLD_NEXT, "TPlayerDestroy");
    if (p == g_player)
        g_player = 0;
    fprintf(stderr, "[seekfix] player destroyed\n");
    return real_destroy(p);
}
