#!/bin/bash
set -e

# Script to set up Python environment, install xcaddy, and optionally build caddy with caddy-snake plugin
#
# Usage:
#   setup-python-and-build.sh [OPTIONS]
#
# Options:
#   --python-version VERSION    Python version to install (e.g., 3.13, 3.13-nogil)
#                               Nogil is automatically detected if version contains "-nogil"
#   --go-version VERSION        Go version to install (default: 1.25.0)
#   --install-go                Install Go (default: true, checks if Go is already installed)
#   --skip-go-install           Skip Go installation (assumes Go is already installed)
#   --extra-packages PACKAGES   Additional apt packages to install (space-separated)
#   --install-xcaddy            Install xcaddy (default: true)
#   --build-caddy               Build caddy with caddy-snake plugin
#   --caddy-snake-path PATH     Path to caddy-snake module (default: .)
#   --output-caddy-path PATH    Move built caddy binary to this path (optional)
#   --skip-python-setup         Skip Python environment setup
#   --working-dir DIR           Working directory for build (optional)

PYTHON_VERSION=""
GO_VERSION="1.25.0"
INSTALL_GO=true
SKIP_GO_INSTALL=false
EXTRA_PACKAGES=""
EXTRA_PACKAGES_SET=false
INSTALL_XCADDY=true
BUILD_CADDY=false
CADDY_SNAKE_PATH="."
OUTPUT_CADDY_PATH=""
SKIP_PYTHON_SETUP=false
WORKING_DIR=""

# Parse arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    --python-version)
      PYTHON_VERSION="$2"
      shift 2
      ;;
    --go-version)
      GO_VERSION="$2"
      shift 2
      ;;
    --install-go)
      INSTALL_GO=true
      SKIP_GO_INSTALL=false
      shift
      ;;
    --skip-go-install)
      SKIP_GO_INSTALL=true
      INSTALL_GO=false
      shift
      ;;
    --extra-packages)
      EXTRA_PACKAGES="$2"
      EXTRA_PACKAGES_SET=true
      shift 2
      ;;
    --install-xcaddy)
      INSTALL_XCADDY=true
      shift
      ;;
    --no-install-xcaddy)
      INSTALL_XCADDY=false
      shift
      ;;
    --build-caddy)
      BUILD_CADDY=true
      shift
      ;;
    --caddy-snake-path)
      CADDY_SNAKE_PATH="$2"
      shift 2
      ;;
    --output-caddy-path)
      OUTPUT_CADDY_PATH="$2"
      shift 2
      ;;
    --skip-python-setup)
      SKIP_PYTHON_SETUP=true
      shift
      ;;
    --working-dir)
      WORKING_DIR="$2"
      shift 2
      ;;
    *)
      echo "Unknown option: $1"
      exit 1
      ;;
  esac
done

# Set working directory if specified
if [ -n "$WORKING_DIR" ]; then
  cd "$WORKING_DIR"
fi

# Detect if this is a nogil version
NOGIL=false
if [[ "$PYTHON_VERSION" == *"-nogil"* ]]; then
  NOGIL=true
fi

# Detect if we're running as root (e.g., in Docker)
# If we are, we don't need sudo
SUDO=""
if [ "$(id -u)" -ne 0 ]; then
  SUDO="sudo"
fi

# Detect architecture for pkgconfig path
# Try to find the pkgconfig directory that exists
PKG_CONFIG_DIR=""
for dir in /usr/lib/x86_64-linux-gnu/pkgconfig /usr/lib/aarch64-linux-gnu/pkgconfig /usr/lib/arm-linux-gnueabihf/pkgconfig; do
  if [ -d "$dir" ]; then
    PKG_CONFIG_DIR="$dir"
    break
  fi
done
# Fallback to x86_64 if nothing found (will be created if needed)
if [ -z "$PKG_CONFIG_DIR" ]; then
  PKG_CONFIG_DIR="/usr/lib/x86_64-linux-gnu/pkgconfig"
fi

# Setup Python environment
if [ "$SKIP_PYTHON_SETUP" = false ] && [ -n "$PYTHON_VERSION" ]; then
  export DEBIAN_FRONTEND=noninteractive
  
  # Base packages
  BASE_PACKAGES="software-properties-common"
  
  if [ "$NOGIL" = true ]; then
    # Nogil setup
    $SUDO apt-get update -yyqq
    $SUDO apt-get install -yyqq $BASE_PACKAGES valgrind gcc build-essential $EXTRA_PACKAGES
    $SUDO add-apt-repository -y ppa:deadsnakes/ppa
    $SUDO apt-get install -yyqq python${PYTHON_VERSION} python3.13-dev
    SOURCE_PC="${PKG_CONFIG_DIR}/python-${PYTHON_VERSION}-embed.pc"
    TARGET_PC="${PKG_CONFIG_DIR}/python3-embed.pc"
    if [ -f "$SOURCE_PC" ] && [ "$SOURCE_PC" != "$TARGET_PC" ]; then
      $SUDO mv "$SOURCE_PC" "$TARGET_PC"
    elif [ -f "$TARGET_PC" ]; then
      echo "pkgconfig file already exists at $TARGET_PC"
    fi
  else
    # Standard Python setup
    $SUDO apt-get update -yyqq
    
    # If building caddy, we need gcc, build-essential, and pkg-config for CGO
    BUILD_PACKAGES=""
    if [ "$BUILD_CADDY" = true ]; then
      BUILD_PACKAGES="gcc build-essential pkg-config"
    fi
    
    # Default extra packages if not explicitly set, or if explicitly set to non-empty
    if [ "$EXTRA_PACKAGES_SET" = false ]; then
      # Not set at all, use defaults
      EXTRA_PACKAGES="valgrind time"
    elif [ -n "$EXTRA_PACKAGES" ]; then
      # Explicitly set to non-empty, add defaults
      EXTRA_PACKAGES="valgrind time $EXTRA_PACKAGES"
    fi
    # If EXTRA_PACKAGES_SET is true but EXTRA_PACKAGES is empty, don't add defaults
    
    if [ -n "$EXTRA_PACKAGES" ]; then
      $SUDO apt-get install -yyqq $BASE_PACKAGES $BUILD_PACKAGES $EXTRA_PACKAGES
    else
      $SUDO apt-get install -yyqq $BASE_PACKAGES $BUILD_PACKAGES
    fi
    $SUDO add-apt-repository -y ppa:deadsnakes/ppa
    $SUDO apt-get install -yyqq python${PYTHON_VERSION}-dev python${PYTHON_VERSION}-venv
    # Ensure pkgconfig directory exists
    $SUDO mkdir -p "${PKG_CONFIG_DIR}"
    # Try to find and copy the pkgconfig file
    PC_FILE=""
    TARGET_PC="${PKG_CONFIG_DIR}/python3-embed.pc"
    
    # Check if target already exists and is correct
    if [ -f "$TARGET_PC" ]; then
      echo "pkgconfig file already exists at $TARGET_PC"
    else
      # Try to find source pkgconfig file
      for pc_path in "${PKG_CONFIG_DIR}/python-${PYTHON_VERSION}-embed.pc" \
                     "/usr/lib/python${PYTHON_VERSION}/config-${PYTHON_VERSION}/python-embed.pc" \
                     "/usr/lib/python${PYTHON_VERSION}/config-${PYTHON_VERSION}*/python-embed.pc"; do
        # Handle glob patterns
        for found_file in $pc_path; do
          if [ -f "$found_file" ] && [ "$found_file" != "$pc_path" ] || [ -f "$pc_path" ]; then
            if [ -f "$found_file" ]; then
              PC_FILE="$found_file"
            elif [ -f "$pc_path" ]; then
              PC_FILE="$pc_path"
            fi
            break 2
          fi
        done
      done
      
      if [ -n "$PC_FILE" ] && [ -f "$PC_FILE" ]; then
        # Check if source and destination are the same file
        if [ "$PC_FILE" != "$TARGET_PC" ]; then
          $SUDO cp "$PC_FILE" "$TARGET_PC"
        else
          echo "Source and destination are the same file, skipping copy"
        fi
      else
        # Create pkgconfig file manually using python-config
        PYTHON_BIN="/usr/bin/python${PYTHON_VERSION}"
        if [ -f "$PYTHON_BIN" ]; then
          PREFIX=$($PYTHON_BIN -c "import sys; print(sys.prefix)" 2>/dev/null || echo "/usr")
          VERSION=$($PYTHON_BIN -c "import sys; print(f'{sys.version_info.major}.{sys.version_info.minor}')" 2>/dev/null || echo "${PYTHON_VERSION}")
          LIBDIR="${PREFIX}/lib"
          INCLUDEDIR="${PREFIX}/include/python${VERSION}"
          # Try to find actual lib directory
          if [ -d "${PREFIX}/lib/python${VERSION}/config-${VERSION}" ]; then
            LIBDIR="${PREFIX}/lib/python${VERSION}/config-${VERSION}"
          fi
          $SUDO sh -c "cat > ${PKG_CONFIG_DIR}/python3-embed.pc << 'EOF'
prefix=${PREFIX}
exec_prefix=\${prefix}
libdir=\${prefix}/lib
includedir=${INCLUDEDIR}

Name: Python
Description: Python library
Version: ${VERSION}
Libs: -L\${libdir} -lpython${VERSION}
Cflags: -I${INCLUDEDIR}
EOF"
        fi
      fi
    fi
    # Set PKG_CONFIG_PATH so pkg-config can find the file
    export PKG_CONFIG_PATH="${PKG_CONFIG_DIR}:${PKG_CONFIG_PATH}"
  fi
fi

# Install Go if needed
if [ "$INSTALL_GO" = true ] && [ "$SKIP_GO_INSTALL" = false ]; then
  # Check if Go is already installed
  if command -v go >/dev/null 2>&1; then
    INSTALLED_GO_VERSION=$(go version | awk '{print $3}' | sed 's/go//')
    echo "Go is already installed: $INSTALLED_GO_VERSION"
    INSTALL_GO=false
  fi
  
  if [ "$INSTALL_GO" = true ]; then
    export DEBIAN_FRONTEND=noninteractive
    
    # Install wget and tar if not already available
    if ! command -v wget >/dev/null 2>&1 || ! command -v tar >/dev/null 2>&1; then
      $SUDO apt-get update -yyqq
      $SUDO apt-get install -yyqq wget tar
    fi
    
    # Detect architecture for Go download
    ARCH=""
    if command -v dpkg >/dev/null 2>&1; then
      ARCH=$(dpkg --print-architecture 2>/dev/null || echo "")
    fi
    if [ -z "$ARCH" ]; then
      ARCH=$(uname -m)
      if [ "$ARCH" = "x86_64" ]; then
        ARCH="amd64"
      elif [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then
        ARCH="arm64"
      fi
    fi
    
    # Map architecture names
    GO_ARCH=""
    if [ "$ARCH" = "amd64" ] || [ "$ARCH" = "x86_64" ]; then
      GO_ARCH="amd64"
    elif [ "$ARCH" = "arm64" ] || [ "$ARCH" = "aarch64" ]; then
      GO_ARCH="arm64"
    else
      echo "Unsupported architecture for Go: $ARCH"
      exit 1
    fi
    
    # Download and install Go
    echo "Installing Go ${GO_VERSION} for ${GO_ARCH}..."
    cd /tmp
    $SUDO rm -f go*.tar.gz
    wget -q "https://dl.google.com/go/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz"
    $SUDO tar -C /usr/local -xzf "go${GO_VERSION}.linux-${GO_ARCH}.tar.gz"
    rm -f "go${GO_VERSION}.linux-${GO_ARCH}.tar.gz"
    
    # Add Go to PATH
    export PATH="/usr/local/go/bin:$PATH"
    
    # Verify installation
    if command -v go >/dev/null 2>&1; then
      echo "Go installed successfully: $(go version)"
    else
      echo "Warning: Go installation may have failed. PATH may need to include /usr/local/go/bin"
      export PATH="/usr/local/go/bin:$PATH"
    fi
  fi
fi

# Ensure Go is in PATH (in case it was installed but PATH wasn't updated)
if command -v go >/dev/null 2>&1; then
  # Go is available
  :
elif [ -f "/usr/local/go/bin/go" ]; then
  export PATH="/usr/local/go/bin:$PATH"
elif [ -n "$GOROOT" ] && [ -f "$GOROOT/bin/go" ]; then
  export PATH="$GOROOT/bin:$PATH"
fi

# Install xcaddy
if [ "$INSTALL_XCADDY" = true ]; then
  go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest
  # Add Go bin directory to PATH if not already there
  if [ -n "$GOPATH" ]; then
    export PATH="$PATH:$GOPATH/bin"
  else
    export PATH="$PATH:$HOME/go/bin"
  fi
fi

# Build caddy with caddy-snake plugin
if [ "$BUILD_CADDY" = true ]; then
  # Find xcaddy binary
  XCADDY_BIN="xcaddy"
  if [ -n "$GOPATH" ] && [ -f "$GOPATH/bin/xcaddy" ]; then
    XCADDY_BIN="$GOPATH/bin/xcaddy"
  elif [ -f "$HOME/go/bin/xcaddy" ]; then
    XCADDY_BIN="$HOME/go/bin/xcaddy"
  fi
  
  # Ensure PKG_CONFIG_PATH is set for the build
  if [ -n "$PKG_CONFIG_DIR" ] && [ -d "$PKG_CONFIG_DIR" ]; then
    export PKG_CONFIG_PATH="${PKG_CONFIG_DIR}:${PKG_CONFIG_PATH}"
  fi
  
  if [ -z "$CADDY_SNAKE_PATH" ] || [ "$CADDY_SNAKE_PATH" = "." ]; then
    # Download from GitHub
    CGO_ENABLED=1 PKG_CONFIG_PATH="${PKG_CONFIG_PATH}" $XCADDY_BIN build --with github.com/mliezun/caddy-snake
  else
    # Use local path
    CGO_ENABLED=1 PKG_CONFIG_PATH="${PKG_CONFIG_PATH}" $XCADDY_BIN build --with github.com/mliezun/caddy-snake=${CADDY_SNAKE_PATH}
  fi
  
  # Move caddy binary to output path if specified
  if [ -n "$OUTPUT_CADDY_PATH" ]; then
    CADDY_FOUND=""
    # Try common locations where xcaddy builds caddy
    for caddy_path in "./caddy" \
                      "/root/go/bin/caddy" \
                      "$HOME/go/bin/caddy" \
                      "$(pwd)/caddy"; do
      if [ -f "$caddy_path" ]; then
        CADDY_FOUND="$caddy_path"
        break
      fi
    done
    
    # If not found in common locations, search for it
    if [ -z "$CADDY_FOUND" ]; then
      CADDY_FOUND=$(find /root /tmp "$(pwd)" -maxdepth 3 -name "caddy" -type f 2>/dev/null | head -1)
    fi
    
    if [ -n "$CADDY_FOUND" ] && [ -f "$CADDY_FOUND" ]; then
      # Create parent directory if it doesn't exist
      OUTPUT_DIR=$(dirname "$OUTPUT_CADDY_PATH")
      if [ ! -d "$OUTPUT_DIR" ]; then
        $SUDO mkdir -p "$OUTPUT_DIR"
      fi
      $SUDO mv "$CADDY_FOUND" "$OUTPUT_CADDY_PATH"
      $SUDO chmod +x "$OUTPUT_CADDY_PATH"
      echo "Caddy binary moved to: $OUTPUT_CADDY_PATH"
    else
      echo "Warning: Could not find caddy binary to move to $OUTPUT_CADDY_PATH"
      exit 1
    fi
  fi
fi

