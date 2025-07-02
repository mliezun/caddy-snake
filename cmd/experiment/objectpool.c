#include <stdio.h>
#include <stdlib.h>
#include <time.h>

// Define the object structure for a doubly linked list
typedef struct Object {
    int data; // Example field
    struct Object *next;
    struct Object *prev;
    int in_use; // Flag for whether the object is in use
} Object;

// Define the pool structure
typedef struct {
    Object *head;           // Points to first unused object
    Object *tail;           // Points to last object in pool
    Object *first_used;     // Points to first used object (for O(1) tracking)
    size_t size;            // Current size of the pool
    size_t used_count;      // Number of objects currently in use
} ObjectPool;

// Initialize the pool
ObjectPool *initialize_pool(size_t initial_size) {
    ObjectPool *pool = malloc(sizeof(ObjectPool));
    pool->head = NULL;
    pool->tail = NULL;
    pool->first_used = NULL;
    pool->size = 0;
    pool->used_count = 0;

    // Add initial objects to the pool
    for (size_t i = 0; i < initial_size; i++) {
        Object *new_obj = malloc(sizeof(Object));
        new_obj->data = 0;
        new_obj->in_use = 0;
        new_obj->next = pool->head;
        new_obj->prev = NULL;

        if (pool->head != NULL) {
            pool->head->prev = new_obj;
        }
        pool->head = new_obj;

        if (pool->tail == NULL) {
            pool->tail = new_obj; // Set tail for the first element
        }
        pool->size++;
    }

    return pool;
}

// Get an unused object - always O(1)
Object *get_object(ObjectPool *pool) {
    // If no unused objects available, expand pool by just one
    if (pool->head == NULL || pool->head->in_use) {
        Object *new_obj = malloc(sizeof(Object));
        new_obj->data = 0;
        new_obj->in_use = 0;
        new_obj->next = pool->head;
        new_obj->prev = NULL;

        if (pool->head != NULL) {
            pool->head->prev = new_obj;
        }
        pool->head = new_obj;

        if (pool->tail == NULL) {
            pool->tail = new_obj;
        }
        pool->size++;
    }

    // Get the first unused object (always at head)
    Object *obj = pool->head;
    obj->in_use = 1;

    // Move head to next unused object
    pool->head = obj->next;
    if (pool->head != NULL) {
        pool->head->prev = NULL;
    }

    // Add to used section (insert after last used object or at beginning if no used objects)
    if (pool->first_used == NULL) {
        // This is the first used object
        pool->first_used = obj;
        obj->next = NULL;
        obj->prev = NULL;
    } else {
        // Insert at the beginning of used section
        obj->next = pool->first_used;
        obj->prev = NULL;
        pool->first_used->prev = obj;
        pool->first_used = obj;
    }

    pool->used_count++;
    return obj;
}

// Release an object and place it at the front (unused section)
void release_object(ObjectPool *pool, Object *obj) {
    if (!obj->in_use) {
        return; // Already released
    }

    obj->in_use = 0;
    pool->used_count--;

    // Remove from used section
    if (obj->prev != NULL) {
        obj->prev->next = obj->next;
    } else {
        // This was the first used object
        pool->first_used = obj->next;
    }

    if (obj->next != NULL) {
        obj->next->prev = obj->prev;
    }

    // Add to front of unused section (become new head)
    obj->next = pool->head;
    obj->prev = NULL;

    if (pool->head != NULL) {
        pool->head->prev = obj;
    }
    pool->head = obj;

    // Update tail if this was the only object
    if (pool->tail == NULL) {
        pool->tail = obj;
    }
}

// Alternative simpler approach using a free list
typedef struct SimpleObjectPool {
    Object *free_list;      // Stack of free objects
    Object *all_objects;    // List of all allocated objects for cleanup
    size_t total_allocated;
} SimpleObjectPool;

SimpleObjectPool *initialize_simple_pool(size_t initial_size) {
    SimpleObjectPool *pool = malloc(sizeof(SimpleObjectPool));
    pool->free_list = NULL;
    pool->all_objects = NULL;
    pool->total_allocated = 0;

    // Pre-allocate objects
    for (size_t i = 0; i < initial_size; i++) {
        Object *obj = malloc(sizeof(Object));
        obj->data = 0;
        obj->in_use = 0;

        // Add to free list (stack)
        obj->next = pool->free_list;
        pool->free_list = obj;

        // Add to all_objects list for cleanup
        obj->prev = pool->all_objects;
        pool->all_objects = obj;

        pool->total_allocated++;
    }

    return pool;
}

Object *get_simple_object(SimpleObjectPool *pool) {
    if (pool->free_list == NULL) {
        // Allocate new object
        Object *obj = malloc(sizeof(Object));
        obj->data = 0;
        obj->in_use = 1;

        // Add to all_objects for cleanup
        obj->prev = pool->all_objects;
        pool->all_objects = obj;
        pool->total_allocated++;

        return obj;
    }

    // Pop from free list
    Object *obj = pool->free_list;
    pool->free_list = obj->next;
    obj->in_use = 1;

    return obj;
}

void release_simple_object(SimpleObjectPool *pool, Object *obj) {
    if (!obj->in_use) return;

    obj->in_use = 0;

    // Push to free list
    obj->next = pool->free_list;
    pool->free_list = obj;
}

void cleanup_simple_pool(SimpleObjectPool *pool) {
    Object *current = pool->all_objects;
    while (current != NULL) {
        Object *to_free = current;
        current = current->prev;
        free(to_free);
    }
    free(pool);
}

int main() {
    printf("Testing optimized object pool...\n");

    // Test with original pool structure
    ObjectPool *pool = initialize_pool(16);
    size_t element_count = 1<<20;
    printf("element_count: %zu\n", element_count);
    Object **list = malloc(sizeof(Object *) * element_count);

    clock_t start_time = clock();

    for (size_t i = 0; i < element_count; i++) {
        list[i] = get_object(pool);
        list[i]->data = i;
    }

    // Release objects
    for (size_t i = 0; i < element_count; i++) {
        release_object(pool, list[i]);
    }

    clock_t end_time = clock();
    double elapsed = ((double)(end_time - start_time)) / CLOCKS_PER_SEC;

    printf("Optimized pool - Total time: %.4f seconds\n", elapsed);
    printf("Final pool size: %zu\n", pool->size);

    // Clean up original pool
    Object *current = pool->head;
    while (current != NULL) {
        Object *to_free = current;
        current = current->next;
        free(to_free);
    }
    // Also clean up used objects if any remain
    current = pool->first_used;
    while (current != NULL) {
        Object *to_free = current;
        current = current->next;
        free(to_free);
    }
    free(pool);

    printf("\nTesting simple pool (recommended)...\n");

    // Test with simpler approach
    SimpleObjectPool *simple_pool = initialize_simple_pool(16);

    start_time = clock();

    for (size_t i = 0; i < element_count; i++) {
        list[i] = get_simple_object(simple_pool);
        list[i]->data = i;
    }

    // Release objects
    for (size_t i = 0; i < element_count; i++) {
        release_simple_object(simple_pool, list[i]);
    }

    end_time = clock();
    elapsed = ((double)(end_time - start_time)) / CLOCKS_PER_SEC;

    printf("Simple pool - Total time: %.4f seconds\n", elapsed);
    printf("Total allocated: %zu\n", simple_pool->total_allocated);

    cleanup_simple_pool(simple_pool);
    free(list);
    return 0;
}
