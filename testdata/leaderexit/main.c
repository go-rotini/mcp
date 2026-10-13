/* On SIGTERM a worker thread takes 0.5s to clean up, writes the file named by argv[1] and
   exits. The main thread exits at once, so the process shows as a zombie while the worker
   runs. */
#include <pthread.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>

static volatile sig_atomic_t term;
static const char *mark;

static void on_term(int sig) { (void)sig; term = 1; }

static void *worker(void *arg) {
	(void)arg;
	while (!term) usleep(10000);
	usleep(500000);
	FILE *f = fopen(mark, "w");
	if (f) { fputs("ok\n", f); fclose(f); }
	exit(0);
}

int main(int argc, char **argv) {
	if (argc < 2) return 2;
	mark = argv[1];
	signal(SIGTERM, on_term);
	pthread_t t;
	pthread_create(&t, 0, worker, 0);
	pthread_exit(0);
}
