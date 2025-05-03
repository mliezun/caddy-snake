// package main

// import (
// 	"context"
// 	"fmt"
// 	"io/ioutil"
// 	"log"

// 	"github.com/tetratelabs/wazero"
// 	"github.com/tetratelabs/wazero/api"
// )

// func main() {
// 	ctx := context.Background()
// 	r := wazero.NewRuntime(ctx)
// 	defer r.Close(ctx)

// 	// Instantiate a Go-defined module named "env" that exports a function to
// 	// log to the console.
// 	_, err := r.NewHostModuleBuilder("env").
// 		NewFunctionBuilder().WithFunc(capture).Export("capture_stderr").NewFunctionBuilder().WithFunc(capture).Export("restore_stderr").
// 		Instantiate(ctx)
// 	if err != nil {
// 		log.Panicln(err)
// 	}

// 	r.NewHostModuleBuilder("GOT.func").
// 	_, err = r.NewHostModuleBuilder("GOT.func").NewFunctionBuilder()..Export("__cxa_end_catch").Instantiate(ctx)
// 	if err != nil {
// 		log.Panicln(err)
// 	}
// 	_, err = r.NewHostModuleBuilder("GOT.mem").NewFunctionBuilder().WithFunc(capture).Export("__heap_base").Instantiate(ctx)
// 	if err != nil {
// 		log.Panicln(err)
// 	}

// 	source, _ := ioutil.ReadFile("/Users/mliezun/Downloads/pyodide/pyodide.asm.wasm")
// 	fmt.Println("Read source")
// 	m, err := r.Instantiate(ctx, source)
// 	fmt.Println("r.Instantiate", err)
// 	defer m.Close(ctx)
// 	fmt.Println("Read wasm")
// 	run_string := m.ExportedFunction("PyRun_SimpleString")
// 	fmt.Println(run_string)
// 	fmt.Println("Workgin")
// }

// func capture(_ context.Context, m api.Module) {}

// func logString(_ context.Context, m api.Module, offset, byteCount uint32) {
// 	buf, ok := m.Memory().Read(offset, byteCount)
// 	if !ok {
// 		log.Panicf("Memory.Read(%d, %d) out of range", offset, byteCount)
// 	}
// 	fmt.Println(string(buf))
// }
