// Minimal Rust library for maturin to build platform-specific wheels
// This allows us to package the caddy binary without needing cibuildwheel

use pyo3::prelude::*;

/// A simple Python module that provides access to the caddy binary
#[pymodule]
fn caddysnake(_py: Python, m: Bound<PyModule>) -> PyResult<()> {
    // This is a minimal module - the actual functionality is in cli.py
    // We just need this to satisfy maturin's requirements for a Rust module
    Ok(())
}
