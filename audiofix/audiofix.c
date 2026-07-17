/* libyt_audiofix: LD_PRELOAD warmth EQ for the Brick's tinny speakers.
 *
 * The sun50iw10 codec driver exposes no hardware EQ/DRC, so we filter the PCM
 * in the player process instead: interpose snd_pcm_writei and run two biquads
 * per channel (RBJ cookbook):
 *   - peaking cut  -4.5 dB @ 3.2 kHz, Q 1.1  (kills the "tinny" resonance)
 *   - low shelf    +4.5 dB @ 400 Hz          (adds body the tiny driver CAN make)
 * Rate/format/channels are captured from snd_pcm_hw_params; anything that is
 * not interleaved S16_LE passes through untouched (logged once).
 *
 * Build: aarch64-linux-gnu-gcc -shared -fPIC -O2 audiofix.c -o libyt_audiofix.so -ldl -lm
 * Use:   LD_PRELOAD="...libyt_rectfix.so /mnt/SDCARD/Videos/libyt_audiofix.so" tplayerdemo
 */
#define _GNU_SOURCE
#include <dlfcn.h>
#include <math.h>
#include <stdio.h>
#include <stdint.h>

#define SND_PCM_FORMAT_S16_LE 2

typedef struct {
    double b0, b1, b2, a1, a2;
    double z1[2], z2[2]; /* per-channel state */
} biquad;

static biquad eq_peak, eq_shelf;
static unsigned int g_rate = 0, g_channels = 2;
static int g_format = -1;
static int g_active = 0;

static void biquad_peaking(biquad *f, double fs, double f0, double q, double db)
{
    double A = pow(10.0, db / 40.0), w = 2 * M_PI * f0 / fs;
    double alpha = sin(w) / (2 * q), c = cos(w);
    double a0 = 1 + alpha / A;
    f->b0 = (1 + alpha * A) / a0;
    f->b1 = (-2 * c) / a0;
    f->b2 = (1 - alpha * A) / a0;
    f->a1 = (-2 * c) / a0;
    f->a2 = (1 - alpha / A) / a0;
}

static void biquad_lowshelf(biquad *f, double fs, double f0, double slope, double db)
{
    double A = pow(10.0, db / 40.0), w = 2 * M_PI * f0 / fs;
    double c = cos(w);
    double alpha = sin(w) / 2 * sqrt((A + 1 / A) * (1 / slope - 1) + 2);
    double sq = 2 * sqrt(A) * alpha;
    double a0 = (A + 1) + (A - 1) * c + sq;
    f->b0 = A * ((A + 1) - (A - 1) * c + sq) / a0;
    f->b1 = 2 * A * ((A - 1) - (A + 1) * c) / a0;
    f->b2 = A * ((A + 1) - (A - 1) * c - sq) / a0;
    f->a1 = -2 * ((A - 1) + (A + 1) * c) / a0;
    f->a2 = ((A + 1) + (A - 1) * c - sq) / a0;
}

static inline double biquad_run(biquad *f, int ch, double x)
{
    double y = f->b0 * x + f->z1[ch];
    f->z1[ch] = f->b1 * x - f->a1 * y + f->z2[ch];
    f->z2[ch] = f->b2 * x - f->a2 * y;
    return y;
}

__attribute__((constructor)) static void audiofix_init(void)
{
    fprintf(stderr, "[audiofix] loaded\n");
}

/* capture negotiated stream params right after the player sets them */
int snd_pcm_hw_params(void *pcm, void *params)
{
    static int (*real)(void *, void *) = 0;
    static int (*get_rate)(const void *, unsigned int *, int *) = 0;
    static int (*get_channels)(const void *, unsigned int *) = 0;
    static int (*get_format)(const void *, int *) = 0;
    if (!real) {
        real = (int (*)(void *, void *))dlsym(RTLD_NEXT, "snd_pcm_hw_params");
        get_rate = (int (*)(const void *, unsigned int *, int *))dlsym(RTLD_NEXT, "snd_pcm_hw_params_get_rate");
        get_channels = (int (*)(const void *, unsigned int *))dlsym(RTLD_NEXT, "snd_pcm_hw_params_get_channels");
        get_format = (int (*)(const void *, int *))dlsym(RTLD_NEXT, "snd_pcm_hw_params_get_format");
    }
    int rc = real(pcm, params);
    if (rc == 0 && get_rate && get_channels && get_format) {
        int dir = 0;
        get_rate(params, &g_rate, &dir);
        get_channels(params, &g_channels);
        get_format(params, &g_format);
        if (g_rate > 0 && g_channels >= 1 && g_channels <= 2 &&
            g_format == SND_PCM_FORMAT_S16_LE) {
            biquad_peaking(&eq_peak, g_rate, 3200.0, 1.1, -4.5);
            biquad_lowshelf(&eq_shelf, g_rate, 400.0, 0.9, 4.5);
            g_active = 1;
            fprintf(stderr, "[audiofix] EQ active: %uHz %uch S16\n", g_rate, g_channels);
        } else {
            g_active = 0;
            fprintf(stderr, "[audiofix] passthrough (rate=%u ch=%u fmt=%d)\n",
                    g_rate, g_channels, g_format);
        }
    }
    return rc;
}

long snd_pcm_writei(void *pcm, const void *buffer, unsigned long frames)
{
    static long (*real)(void *, const void *, unsigned long) = 0;
    static int logged = 0;
    if (!real)
        real = (long (*)(void *, const void *, unsigned long))dlsym(RTLD_NEXT, "snd_pcm_writei");
    if (g_active && buffer && frames > 0) {
        int16_t *s = (int16_t *)buffer;
        unsigned long n = frames * g_channels;
        for (unsigned long i = 0; i < n; i++) {
            int ch = (g_channels == 2) ? (int)(i & 1) : 0;
            double x = (double)s[i];
            x = biquad_run(&eq_shelf, ch, x);
            x = biquad_run(&eq_peak, ch, x);
            if (x > 32767.0) x = 32767.0;
            if (x < -32768.0) x = -32768.0;
            s[i] = (int16_t)lrint(x);
        }
        if (!logged) {
            logged = 1;
            fprintf(stderr, "[audiofix] filtering via writei\n");
        }
    }
    return real(pcm, buffer, frames);
}
