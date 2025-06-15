package main

// #cgo pkg-config: python3-embed
// #include "sharedmem.c"
import "C"
import (
	"fmt"
	"time"
)

func main() {
	qctx := C.setup_queue()
	buffer := C.malloc(C.MSG_SIZE * C.sizeof_char)
	start := time.Now()
	for i := 0; i < int(C.MSG_COUNT); i++ {
		C.produce_message(qctx, (*C.uint8_t)(buffer))
	}
	C.cleanup_queue(qctx)
	end := time.Now()
	fmt.Printf("Time taken: %v\n", end.Sub(start))
	// C.MSG_COUNT * C.MSG_SIZE is the amount of bytes passed on each message
	// calculate the total amount of bytes passed on each message
	totalBytes := int64(C.MSG_COUNT) * int64(C.MSG_SIZE)
	fmt.Printf("Total bytes passed: %d\n", totalBytes)
	// Calculate throughput in megabytes per second
	throughput := float64(totalBytes) / float64(end.Sub(start).Seconds()) / 1e6
	fmt.Printf("Throughput: %.2f MB/second\n", throughput)
}
