---
title: Configuration Reference
description: Complete reference of all configuration options for the Python directive
---

# Configuration Reference

The `python` directive allows you to serve Python WSGI or ASGI applications through Caddy. It supports both a simple form and a block form with various configuration options.

## Simple Form

The simple form allows you to specify a WSGI application using the `module:variable` pattern:

```caddyfile
python module_name:variable_name
```

For example:
```caddyfile
python myapp:app
```

## Block Form

The block form provides more configuration options:

```caddyfile
python {
    module_wsgi <module_name:variable_name>
    module_asgi <module_name:variable_name>
    lifespan on|off
    working_dir <path>
    venv <path>
}
```

### Subdirectives

#### module_wsgi
Specifies a WSGI application using the module:variable pattern.

```caddyfile
python {
    module_wsgi myapp:app
}
```

#### module_asgi
Specifies an ASGI application using the module:variable pattern.

```caddyfile
python {
    module_asgi myapp:app
}
```

#### lifespan
Controls the ASGI lifespan protocol. Only applicable when using `module_asgi`. Can be either `on` or `off`.

```caddyfile
python {
    module_asgi myapp:app
    lifespan on
}
```

#### working_dir

Sets the working directory for the Python application.

```caddyfile
python {
    module_wsgi myapp:app
    working_dir /path/to/app
}
```

#### venv
Specifies the path to a Python virtual environment.

```caddyfile
python {
    module_wsgi myapp:app
    venv /path/to/venv
}
```

## Notes

- You must specify either `module_wsgi` or `module_asgi`, but not both
- The `lifespan` directive is only used in ASGI mode
- When `working_dir` is specified, the path must exist and be a directory
- When specified, the `venv` path must point to a valid Python virtual environment
- When using the simple form, it's equivalent to using `module_wsgi` in the block form
