---
title: Architecture
description: Technical details about how Caddy-Snake works and its limitations
---

# Architecture

Caddy-Snake is a Caddy plugin that bridges the gap between Caddy's HTTP server and Python web applications. It translates HTTP requests into Python objects and communicates with WSGI or ASGI applications using their respective protocols.



## Request Flow

<img src="/en/latest/img/caddysnake-diagram.png" alt="Example request flow for Django app" width="600"></img>

1. Caddy receives an HTTP request
2. The request is passed to the Caddy-Snake plugin
3. The plugin translates the request into Python objects:
   - For WSGI: Creates a WSGI environment dictionary
   - For ASGI: Creates an ASGI scope dictionary
4. The request is passed to the Python application
5. The Python application processes the request and returns a response
6. The plugin translates the response back to HTTP
7. Caddy sends the response to the client

## Python Integration

The plugin uses Python's C API to embed a Python interpreter within the Caddy process. This integration is achieved through CGO, which allows Go code to call C functions and vice versa.

## Current Limitations

### Single-Threaded Execution

The current implementation is single-threaded. All requests are processed sequentially in the same Python interpreter. This means:

- No parallel processing of requests
- Long-running requests can block other requests
- Not suitable for CPU-intensive applications that require parallel processing

### Shared Interpreter

All Python applications imported by the plugin share the same Python interpreter. This has several implications:

- No isolation between different applications
- Shared memory space
- Potential security concerns for multi-tenant environments
- Applications can potentially interfere with each other

### Security Considerations

Due to the shared interpreter architecture, there are important security considerations:

- Applications can access each other's memory
- One application could potentially modify another application's state
- No resource limits per application
- Not suitable for untrusted code execution

## Future Improvements

Potential areas for improvement include:

- Multi-threaded request processing
- Process isolation between applications
- Resource limits per application
- Better security boundaries
- Support for multiple Python versions

## Technical Details

### WSGI Implementation

The WSGI implementation follows the WSGI specification (PEP 3333). It:

- Creates a WSGI environment dictionary with request details
- Calls the application with the environment and a start_response callback
- Handles the response and writes it back to the client

### ASGI Implementation

The ASGI implementation follows the ASGI specification. It:

- Creates an ASGI scope dictionary with request details
- Handles the ASGI protocol lifecycle
- Supports WebSocket connections
- Manages the lifespan protocol

### Memory Management

The plugin carefully manages memory between Go and Python:

- Uses CGO to safely pass data between Go and Python
- Properly handles Python object reference counting
- Cleans up resources after each request
- Manages WebSocket connections and their lifecycle 
