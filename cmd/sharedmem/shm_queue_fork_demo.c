#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <fcntl.h>
#include <sys/mman.h>
#include <unistd.h>
#include <stdint.h>
#include <sys/wait.h>
#include <sys/time.h>
#include <errno.h>

#define QUEUE_SIZE (128) // number of slots in the queue (must be > 1 & < MSG_COUNT)
#define MSG_SIZE   (1<<20)
#define MSG_COUNT  (1024l*1024l)
#define SHM_NAME "/my_bigmsg_shm_queue"

typedef struct {
    size_t head;    // Consumer reads
    size_t tail;    // Producer writes
    uint8_t data[QUEUE_SIZE][MSG_SIZE]; // Array of messages
} spsc_queue_t;

// Producer enqueues one message (returns 1 if successful)
int enqueue(spsc_queue_t *q, const uint8_t *msg) {
    size_t next_tail = (q->tail + 1) % QUEUE_SIZE;
    if (next_tail == q->head) {
        // Queue is full
        return 0;
    }
    memcpy(q->data[q->tail], msg, MSG_SIZE);
    __sync_synchronize();
    q->tail = next_tail;
    return 1;
}

// Consumer dequeues one message into msg buffer (returns 1 if successful)
int dequeue(spsc_queue_t *q, uint8_t *msg) {
    if (q->head == q->tail) {
        // Queue is empty
        return 0;
    }
    memcpy(msg, q->data[q->head], MSG_SIZE);
    __sync_synchronize();
    q->head = (q->head + 1) % QUEUE_SIZE;
    return 1;
}

// Timing utility
double now_seconds() {
    struct timeval tv;
    gettimeofday(&tv, NULL);
    return (double)tv.tv_sec + tv.tv_usec * 1e-6;
}

int main() {
    // Shared memory setup
    int shm_fd = shm_open(SHM_NAME, O_CREAT | O_RDWR, 0600);
    if (shm_fd < 0) {
        perror("shm_open");
        exit(1);
    }
    if (ftruncate(shm_fd, sizeof(spsc_queue_t)) != 0) {
        perror("ftruncate");
        exit(1);
    }
    spsc_queue_t *queue = mmap(NULL, sizeof(spsc_queue_t), PROT_READ | PROT_WRITE, MAP_SHARED, shm_fd, 0);
    if (queue == MAP_FAILED) {
        perror("mmap");
        exit(1);
    }
    memset(queue, 0, sizeof(spsc_queue_t)); // Only parent should zero the queue

    // Prepare a single big message for sending
    uint8_t *msg_send = malloc(MSG_SIZE);
    if (!msg_send) {
        fprintf(stderr, "Failed to allocate send buffer\n");
        exit(1);
    }
    memset(msg_send, 0xAB, MSG_SIZE);

    pid_t pid = fork();
    if (pid < 0) {
        perror("fork");
        exit(1);
    }

    if (pid == 0) {
        // ---- CHILD: Consumer ----
        uint8_t *msg_recv = malloc(MSG_SIZE);
        if (!msg_recv) {
            fprintf(stderr, "Consumer failed to allocate recv buffer\n");
            exit(1);
        }

        size_t received = 0;
        double start = now_seconds();

        while (received < MSG_COUNT) {
            if (dequeue(queue, msg_recv)) {
                received++;
                // Optionally, you could check some part of the message for validation here
            } else {
                // Queue empty, wait a little
                usleep(10); // 10 microseconds
            }
        }

        double end = now_seconds();

        size_t total_bytes = MSG_SIZE * MSG_COUNT;
        printf("Consumer summary:\n");
        printf("  Received %zu messages of %d bytes\n", received, MSG_SIZE);
        printf("  Total bytes received: %zu (%.2f MiB)\n", total_bytes, total_bytes / (1024.0 * 1024.0));
        printf("  Elapsed time: %.6f seconds\n", end - start);
        printf("  Throughput: %.2f MiB/s\n", (total_bytes / (1024.0 * 1024.0)) / (end - start));

        free(msg_recv);
        munmap(queue, sizeof(spsc_queue_t));
        close(shm_fd);
        exit(0);
    } else {
        // ---- PARENT: Producer ----
        double start = now_seconds();

        size_t sent = 0;
        while (sent < MSG_COUNT) {
            if (enqueue(queue, msg_send)) {
                sent++;
                // Optionally modify msg_send for each message, if desired
            } else {
                // Queue full, wait
                usleep(10);
            }
        }

        double end = now_seconds();

        // Wait for child to finish
        int status;
        waitpid(pid, &status, 0);

        size_t total_bytes = MSG_SIZE * MSG_COUNT;
        printf("Producer summary:\n");
        printf("  Sent %zu messages of %d bytes\n", sent, MSG_SIZE);
        printf("  Total bytes sent: %zu (%.2f MiB)\n", total_bytes, total_bytes / (1024.0 * 1024.0));
        printf("  Elapsed time: %.6f seconds\n", end - start);
        printf("  Throughput: %.2f MiB/s\n", (total_bytes / (1024.0 * 1024.0)) / (end - start));

        // Cleanup
        munmap(queue, sizeof(spsc_queue_t));
        close(shm_fd);
        shm_unlink(SHM_NAME);
        free(msg_send);

        printf("Total data exchanged between processes: %.2f MiB\n", (2.0 * total_bytes) / (1024.0 * 1024.0));
        printf("Program completed successfully.\n");
        return 0;
    }
}
