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
#include <Python.h>

#define QUEUE_SIZE (128) // number of slots in the queue (must be > 1 & < MSG_COUNT)
#define MSG_SIZE   (1<<20)
#define MSG_COUNT  (1024l)
#define SHM_NAME "/my_bigmsg_shm_queue"

PyObject *write_bytes;

typedef struct {
    size_t head;    // Consumer reads
    size_t tail;    // Producer writes
    uint8_t data[QUEUE_SIZE][MSG_SIZE]; // Array of messages
} spsc_queue_t;

typedef struct {
    spsc_queue_t *queue;
    int shm_fd;
    uint8_t *msg_buffer;
    pid_t consumer_pid;
} queue_context_t;

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

void start_python() {
  PyStatus status;
  PyConfig config;
  PyConfig_InitPythonConfig(&config);
  status = PyConfig_SetString(&config, &config.program_name, L"throughput");
  if (PyStatus_Exception(status)) {
    goto exception;
  }
  status = Py_InitializeFromConfig(&config);
  if (PyStatus_Exception(status)) {
    goto exception;
  }
  PyConfig_Clear(&config);

  PyRun_SimpleString("def write_bytes(bytes):\n"
                     "   return len(bytes)\n");
  PyObject *main_module = PyImport_AddModule("__main__");
  write_bytes = PyObject_GetAttrString(main_module, "write_bytes");

  return;
exception:
  PyConfig_Clear(&config);
  Py_ExitStatusException(status);
}

long send_python_message(char *msg, size_t msg_size) {
  PyObject *py_msg = PyBytes_FromStringAndSize(msg, msg_size);
  PyObject *n = PyObject_CallOneArg(write_bytes, py_msg);
  Py_DECREF(py_msg);
  return PyLong_AS_LONG(n);
}

// Setup function: creates shared memory and initializes queue
queue_context_t* setup_queue() {
    queue_context_t *ctx = malloc(sizeof(queue_context_t));
    if (!ctx) {
        return NULL;
    }

    // Shared memory setup
    ctx->shm_fd = shm_open(SHM_NAME, O_CREAT | O_RDWR, 0600);
    if (ctx->shm_fd < 0) {
        free(ctx);
        return NULL;
    }

    if (ftruncate(ctx->shm_fd, sizeof(spsc_queue_t)) != 0) {
        close(ctx->shm_fd);
        shm_unlink(SHM_NAME);
        free(ctx);
        return NULL;
    }

    ctx->queue = mmap(NULL, sizeof(spsc_queue_t), PROT_READ | PROT_WRITE, MAP_SHARED, ctx->shm_fd, 0);
    if (ctx->queue == MAP_FAILED) {
        close(ctx->shm_fd);
        shm_unlink(SHM_NAME);
        free(ctx);
        return NULL;
    }

    // Initialize queue (only done once during setup)
    memset(ctx->queue, 0, sizeof(spsc_queue_t));

    // Allocate message buffer
    ctx->msg_buffer = malloc(MSG_SIZE);
    if (!ctx->msg_buffer) {
        munmap(ctx->queue, sizeof(spsc_queue_t));
        close(ctx->shm_fd);
        shm_unlink(SHM_NAME);
        free(ctx);
        return NULL;
    }

    // Fork and start consumer process
    ctx->consumer_pid = fork();
    if (ctx->consumer_pid < 0) {
        free(ctx->msg_buffer);
        munmap(ctx->queue, sizeof(spsc_queue_t));
        close(ctx->shm_fd);
        shm_unlink(SHM_NAME);
        free(ctx);
        return NULL;
    }

    if (ctx->consumer_pid == 0) {
        // Child process: Consumer
        uint8_t *msg_recv = malloc(MSG_SIZE);
        if (!msg_recv) {
            exit(1);
        }

        start_python();

        struct timeval start, end;
        gettimeofday(&start, NULL);

        size_t received = 0;
        size_t total_bytes = 0;
        while (received < MSG_COUNT) {
            if (dequeue(ctx->queue, msg_recv)) {
                received++;
                total_bytes += send_python_message((char *)msg_recv, MSG_SIZE);
            } else {
                usleep(10); // Queue empty, wait
            }
        }

        // Calculate throughput in megabytes per second
        gettimeofday(&end, NULL);
        double throughput = ((double)total_bytes / 1024 / 1024) / ((end.tv_sec - start.tv_sec) + (end.tv_usec - start.tv_usec) / 1e6);
        printf("Throughput: %.2f MB/second\n", throughput);

        free(msg_recv);
        munmap(ctx->queue, sizeof(spsc_queue_t));
        close(ctx->shm_fd);
        exit(0);
    }

    // Unlink shared memory object so no other process can access it
    shm_unlink(SHM_NAME);

    // Parent process continues
    return ctx;
}

// Cleanup function: waits for consumer, unmaps shared memory and frees resources
void cleanup_queue(queue_context_t *ctx) {
    if (!ctx) {
        return;
    }

    // Wait for consumer process to finish
    if (ctx->consumer_pid > 0) {
        int status;
        waitpid(ctx->consumer_pid, &status, 0);
    }

    if (ctx->msg_buffer) {
        free(ctx->msg_buffer);
    }

    if (ctx->queue && ctx->queue != MAP_FAILED) {
        munmap(ctx->queue, sizeof(spsc_queue_t));
    }

    if (ctx->shm_fd >= 0) {
        close(ctx->shm_fd);
        shm_unlink(SHM_NAME);
    }

    free(ctx);
}

// Producer function: enqueues a message and waits until successful
int produce_message(queue_context_t *ctx, const uint8_t *data) {
    if (!ctx || !ctx->queue || !data) {
        return -1;
    }

    // Keep trying until enqueue succeeds
    while (!enqueue(ctx->queue, data)) {
        usleep(10); // Wait 10 microseconds before retrying
    }

    return 0; // Success
}
