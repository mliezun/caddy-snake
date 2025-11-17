#!/usr/bin/env python3
import argparse
import sys
import json
import urllib.request
import urllib.error
import os
import time
from pathlib import Path


def levenshtein_distance(s1: str, s2: str) -> int:
    """Calculate the Levenshtein distance between two strings."""
    if len(s1) < len(s2):
        return levenshtein_distance(s2, s1)

    if len(s2) == 0:
        return len(s1)

    previous_row = list(range(len(s2) + 1))
    for i, c1 in enumerate(s1):
        current_row = [i + 1]
        for j, c2 in enumerate(s2):
            insertions = previous_row[j + 1] + 1
            deletions = current_row[j] + 1
            substitutions = previous_row[j] + (c1 != c2)
            current_row.append(min(insertions, deletions, substitutions))
        previous_row = current_row

    return previous_row[-1]


GITHUB_API = "https://api.github.com/repos/astral-sh/python-build-standalone/releases"

# Retry configuration
MAX_RETRIES = 5
INITIAL_BACKOFF = 1  # seconds
MAX_BACKOFF = 60  # seconds


def is_rate_limit_error(e: urllib.error.HTTPError) -> bool:
    """Check if the error is a rate limit error."""
    if e.code == 429:
        return True
    if e.code == 403:
        # Check for rate limit in headers
        headers = e.headers or {}
        if "X-RateLimit-Remaining" in headers:
            remaining = headers.get("X-RateLimit-Remaining", "1")
            try:
                if int(remaining) == 0:
                    return True
            except ValueError:
                pass
        # Check for rate limit message in response
        if "rate limit" in str(e.reason).lower():
            return True
    return False


def get_retry_after(e: urllib.error.HTTPError) -> int | None:
    """Extract retry-after delay from rate limit error."""
    headers = e.headers or {}
    # Check Retry-After header
    if "Retry-After" in headers:
        try:
            return int(headers["Retry-After"])
        except ValueError:
            pass
    # Check X-RateLimit-Reset header
    if "X-RateLimit-Reset" in headers:
        try:
            reset_time = int(headers["X-RateLimit-Reset"])
            current_time = int(time.time())
            delay = reset_time - current_time
            if delay > 0:
                return delay
        except (ValueError, TypeError):
            pass
    return None


def retry_with_backoff(func, *args, **kwargs):
    """Execute a function with exponential backoff retry on rate limit errors."""
    backoff = INITIAL_BACKOFF
    last_exception = None
    
    for attempt in range(MAX_RETRIES):
        try:
            return func(*args, **kwargs)
        except urllib.error.HTTPError as e:
            if is_rate_limit_error(e):
                last_exception = e
                retry_after = get_retry_after(e)
                
                if retry_after is not None:
                    wait_time = min(retry_after, MAX_BACKOFF)
                else:
                    wait_time = min(backoff, MAX_BACKOFF)
                
                if attempt < MAX_RETRIES - 1:
                    print(
                        f"Rate limited. Retrying in {wait_time:.1f} seconds... "
                        f"(attempt {attempt + 1}/{MAX_RETRIES})",
                        file=sys.stderr,
                    )
                    time.sleep(wait_time)
                    backoff *= 2  # Exponential backoff
                else:
                    break
            else:
                # Not a rate limit error, re-raise immediately
                raise
        except urllib.error.URLError as e:
            # Network errors might be transient, retry with backoff
            last_exception = e
            if attempt < MAX_RETRIES - 1:
                wait_time = min(backoff, MAX_BACKOFF)
                print(
                    f"Network error: {e.reason}. Retrying in {wait_time:.1f} seconds... "
                    f"(attempt {attempt + 1}/{MAX_RETRIES})",
                    file=sys.stderr,
                )
                time.sleep(wait_time)
                backoff *= 2
            else:
                break

    # If we get here, all retries failed
    if last_exception:
        raise last_exception
    raise Exception("Failed after all retries")


# Supported architectures
ARCHITECTURES = {
    "aarch64-apple-darwin": "macOS ARM CPU (M1, M2, M3, etc.)",
    "x86_64-apple-darwin": "macOS Intel CPU",
    "i686-pc-windows-msvc": "Windows 32-bit Intel/AMD CPU",
    "x86_64-pc-windows-msvc": "Windows 64-bit Intel/AMD CPU",
    "x86_64-unknown-linux-gnu": "Linux 64-bit Intel/AMD CPU, linked with GNU libc",
    "x86_64-unknown-linux-musl": "Linux 64-bit Intel/AMD CPU, linked with musl libc",
    "aarch64-unknown-linux-gnu": "Linux ARM64 CPUs (AWS Graviton, etc.)",
    "aarch64-unknown-linux-musl": "Linux ARM64 CPUs with musl libc",
    "i686-unknown-linux-gnu": "Linux 32-bit Intel/AMD CPU",
    "i686-unknown-linux-musl": "Linux 32-bit Intel/AMD CPU with musl libc",
    "x86_64_v2-unknown-linux-gnu": "Linux 64-bit Intel/AMD CPU (2008 Nehalem onwards)",
    "x86_64_v2-unknown-linux-musl": "Linux 64-bit Intel/AMD CPU with musl libc (2008 Nehalem onwards)",
    "x86_64_v3-unknown-linux-gnu": "Linux 64-bit Intel/AMD CPU (2013 Haswell onwards)",
    "x86_64_v3-unknown-linux-musl": "Linux 64-bit Intel/AMD CPU with musl libc (2013 Haswell onwards)",
    "x86_64_v4-unknown-linux-gnu": "Linux 64-bit Intel/AMD CPU with AVX-512 (2017 onwards)",
    "x86_64_v4-unknown-linux-musl": "Linux 64-bit Intel/AMD CPU with musl libc and AVX-512 (2017 onwards)",
}

# Build configurations
BUILD_CONFIGS = {
    "freethreaded": "Free-threaded (PEP 703) build without GIL",
    "freethreaded+debug": "Free-threaded build with debug symbols",
    "freethreaded+pgo+lto": "Free-threaded build with profile guided optimization and link-time optimization",
    "freethreaded+lto": "Free-threaded build with link-time optimization",
    "freethreaded+noopt": "Free-threaded build with normal optimization",
    "freethreaded+pgo": "Free-threaded build with profile guided optimization",
    "pgo+lto": "Profile guided optimization and Link-time optimization (fastest)",
    "pgo": "Profile guided optimization only",
    "lto": "Link-time optimization only",
    "noopt": "Normal optimization only",
    "debug": "Debug build without optimization",
}

# Included content types
CONTENT_TYPES = {
    "install_only": "Only files needed for post-build installation",
    "install_only_stripped": "Lightweight version with debug symbols removed (recommended)",
    "full": "All files and artifacts used in build (distributed as .tar.zst)",
}

# Windows-specific variants
WINDOWS_VARIANTS = {
    "shared": "Windows Python standard build with DLLs",
    "static": "Static-linked Python build (fragile, not recommended)",
}


def _fetch_release(url: str):
    """Internal function to fetch release data (used with retry logic)."""
    with urllib.request.urlopen(url) as response:
        if response.status != 200:
            raise urllib.error.HTTPError(
                url, response.status, "HTTP Error", response.headers, None
            )
        return json.loads(response.read().decode("utf-8"))


def get_release(tag: str | None):
    url = f"{GITHUB_API}/latest" if tag == "latest" else f"{GITHUB_API}/tags/{tag}"
    try:
        return retry_with_backoff(_fetch_release, url)
    except urllib.error.HTTPError as e:
        print(f"HTTP Error {e.code}: {e.reason}", file=sys.stderr)
        sys.exit(1)
    except urllib.error.URLError as e:
        print(f"URL Error: {e.reason}", file=sys.stderr)
        sys.exit(1)


def parse_filename(filename: str):
    """Parse a python-build-standalone filename into its components."""
    # Remove file extension - handle both .tar.gz and .tar.zst
    if filename.endswith(".tar.gz"):
        name = filename[:-7]  # Remove .tar.gz
    elif filename.endswith(".tar.zst"):
        name = filename[:-8]  # Remove .tar.zst
    else:
        name = os.path.splitext(filename)[0]

    # Expected format: cpython-{version}+{timestamp}-{architecture}-{build_config}-{content_type}
    # or for Windows: cpython-{version}+{timestamp}-{architecture}-{windows_variant}-{build_config}-{content_type}

    result = {}

    # First, find the version+timestamp part
    if not name.startswith("cpython-"):
        return None

    # Find the first '+' after "cpython-"
    version_start = len("cpython-")
    plus_pos = name.find("+", version_start)
    if plus_pos == -1:
        return None

    # Find the next '-' after the timestamp
    next_dash = name.find("-", plus_pos)
    if next_dash == -1:
        return None

    version_part = name[version_start:next_dash]
    if "+" not in version_part:
        return None

    version, timestamp = version_part.split("+", 1)
    result["version"] = version
    result["timestamp"] = timestamp

    # Now work with the rest of the string
    rest = name[next_dash + 1 :]  # Skip the '-'

    # The structure can be either:
    # 1. architecture-build_config-content_type (with build config)
    # 2. architecture-content_type (without build config)
    #
    # We need to determine which case we have by looking at the content_type
    # Known content types: install_only, install_only_stripped, full

    # Find the last dash to get content_type
    last_dash = rest.rfind("-")
    if last_dash == -1:
        return None

    content_type = rest[last_dash + 1 :]
    result["content_type"] = content_type

    # Check if this is a known content type
    known_content_types = ["install_only", "install_only_stripped", "full"]
    if content_type in known_content_types:
        # This could be either case. We need to check if there's a build config
        # by looking at the remaining part and seeing if it contains known build configs
        remaining = rest[:last_dash]

        # Known build configs that might appear
        known_build_configs = [
            "debug",
            "pgo",
            "lto",
            "pgo+lto",
            "freethreaded+debug",
            "freethreaded+pgo+lto",
            "noopt",
        ]

        # Check if the remaining part ends with a known build config
        has_build_config = False
        for build_config in known_build_configs:
            if remaining.endswith("-" + build_config):
                # This is case 1: architecture-build_config-content_type
                result["build_config"] = build_config
                result["architecture"] = remaining[: -len("-" + build_config)]
                has_build_config = True
                break

        if not has_build_config:
            # This is case 2: architecture-content_type (no build config)
            result["architecture"] = remaining
            result["build_config"] = None
    else:
        # This is case 1: architecture-build_config-content_type
        # Find the second-to-last dash
        second_last_dash = rest.rfind("-", 0, last_dash)
        if second_last_dash == -1:
            return None

        result["build_config"] = rest[second_last_dash + 1 : last_dash]
        result["architecture"] = rest[:second_last_dash]

    # Check if this is a Windows build with variant
    # Windows variants are 'shared' or 'static' and appear before build_config
    arch_parts = result["architecture"].split("-")
    if len(arch_parts) >= 2 and arch_parts[-1] in ["shared", "static"]:
        result["windows_variant"] = arch_parts[-1]
        result["architecture"] = "-".join(arch_parts[:-1])

    return result


def select_asset(
    release_json,
    python_version: str,
    architecture: str,
    build_config: str,
    content_type: str,
    windows_variant: str = None,
):
    """Select the appropriate asset based on the specified criteria."""
    # Handle Windows variants
    expected_architecture = architecture
    if architecture.startswith(("i686-pc-windows-msvc", "x86_64-pc-windows-msvc")):
        if windows_variant:
            expected_architecture = f"{architecture}-{windows_variant}"

    # First try: exact match with specified build config
    matches = []

    for asset in release_json["assets"]:
        parsed = parse_filename(asset["name"])
        if not parsed:
            continue

        # Check if this asset matches our criteria
        matches_criteria = True

        # Python version: substring match (e.g., "3.12" matches "3.12.11")
        if not parsed["version"].startswith(python_version):
            matches_criteria = False

        # Architecture: exact match
        if parsed["architecture"] != expected_architecture:
            matches_criteria = False

        # Build config: exact match
        if parsed.get("build_config") != build_config:
            matches_criteria = False

        # Content type: exact match
        if parsed["content_type"] != content_type:
            matches_criteria = False

        # Windows variant: exact match (if specified)
        if windows_variant:
            if parsed.get("windows_variant") != windows_variant:
                matches_criteria = False
        elif parsed.get("windows_variant"):
            # If we didn't specify a variant but this asset has one, it doesn't match
            matches_criteria = False

        if matches_criteria:
            matches.append(
                (asset["browser_download_url"], asset["name"], parsed["version"])
            )

    if matches:
        # If we have multiple matches (e.g., for partial version like 3.12),
        # select the one with the highest patch version
        if len(matches) > 1:

            def extract_version_tuple(version_str):
                try:
                    return tuple(map(int, version_str.split(".")))
                except ValueError:
                    return (0, 0, 0)

            matches.sort(key=lambda x: extract_version_tuple(x[2]), reverse=True)

        return matches[0][0], matches[0][1]

    # Second try: fallback to assets without build config if exact match failed
    fallback_matches = []

    for asset in release_json["assets"]:
        parsed = parse_filename(asset["name"])
        if not parsed:
            continue

        # Check if this asset matches our criteria (without build config)
        matches_criteria = True

        # Python version: substring match
        if not parsed["version"].startswith(python_version):
            matches_criteria = False

        # Architecture: exact match
        if parsed["architecture"] != expected_architecture:
            matches_criteria = False

        # Build config: must be None (no build config)
        if parsed.get("build_config") is not None:
            matches_criteria = False

        # Content type: exact match
        if parsed["content_type"] != content_type:
            matches_criteria = False

        # Windows variant: exact match (if specified)
        if windows_variant:
            if parsed.get("windows_variant") != windows_variant:
                matches_criteria = False
        elif parsed.get("windows_variant"):
            matches_criteria = False

        if matches_criteria:
            fallback_matches.append(
                (asset["browser_download_url"], asset["name"], parsed["version"])
            )

    if fallback_matches:
        # If we have multiple matches, select the one with the highest patch version
        if len(fallback_matches) > 1:

            def extract_version_tuple(version_str):
                try:
                    return tuple(map(int, version_str.split(".")))
                except ValueError:
                    return (0, 0, 0)

            fallback_matches.sort(
                key=lambda x: extract_version_tuple(x[2]), reverse=True
            )

        return fallback_matches[0][0], fallback_matches[0][1]

    return None, None


def _download_asset_chunk(url: str, dest_path: Path):
    """Internal function to download asset (used with retry logic)."""
    # Clean up any existing partial download before starting
    if dest_path.exists():
        dest_path.unlink()

    with urllib.request.urlopen(url) as response:
        if response.status != 200:
            raise urllib.error.HTTPError(
                url, response.status, "HTTP Error", response.headers, None
            )
        with open(dest_path, "wb") as f:
            while True:
                chunk = response.read(8192)
                if not chunk:
                    break
                f.write(chunk)


def download_asset(url, filename, dest):
    dest_path = Path(dest) / filename
    try:
        retry_with_backoff(_download_asset_chunk, url, dest_path)
    except urllib.error.HTTPError as e:
        print(f"HTTP Error {e.code}: {e.reason}", file=sys.stderr)
        # Clean up partial download on error
        if dest_path.exists():
            dest_path.unlink()
        sys.exit(1)
    except urllib.error.URLError as e:
        print(f"URL Error: {e.reason}", file=sys.stderr)
        # Clean up partial download on error
        if dest_path.exists():
            dest_path.unlink()
        sys.exit(1)
    return dest_path


def validate_architecture(architecture: str) -> bool:
    """Validate that the architecture is supported."""
    return architecture in ARCHITECTURES


def validate_build_config(build_config: str) -> bool:
    """Validate that the build configuration is supported."""
    return build_config in BUILD_CONFIGS


def validate_content_type(content_type: str) -> bool:
    """Validate that the content type is supported."""
    return content_type in CONTENT_TYPES


def validate_windows_variant(variant: str) -> bool:
    """Validate that the Windows variant is supported."""
    return variant in WINDOWS_VARIANTS


def list_available_options():
    """List all available options for each category."""
    print("Available Architectures:")
    for arch, desc in ARCHITECTURES.items():
        print(f"  {arch:<30} - {desc}")

    print("\nAvailable Build Configurations:")
    for config, desc in BUILD_CONFIGS.items():
        print(f"  {config:<15} - {desc}")

    print("\nAvailable Content Types:")
    for content, desc in CONTENT_TYPES.items():
        print(f"  {content:<20} - {desc}")

    print("\nAvailable Windows Variants:")
    for variant, desc in WINDOWS_VARIANTS.items():
        print(f"  {variant:<10} - {desc}")


def main():
    parser = argparse.ArgumentParser(
        prog="pybs",
        description="Fetch python-build-standalone binaries easily",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  pybs latest                                    # Default: Python 3.13, Linux GNU, latest release, stripped
  pybs latest --python-version 3.12              # Python 3.12 instead
  pybs latest --architecture x86_64-apple-darwin # macOS Intel
  pybs latest --build-config pgo+lto            # Fastest build
  pybs latest --content-type full               # Full build with all artifacts
  pybs 20250920 --architecture aarch64-apple-darwin --build-config pgo+lto
  pybs latest --list-options                    # List all available options
        """,
    )

    # Positional arguments
    parser.add_argument("version", help="'latest' or a release tag (e.g. 20250920)")

    # Python version
    parser.add_argument(
        "--python-version", default="3.13", help="Python version (default: 3.13)"
    )

    # Architecture selection
    parser.add_argument(
        "--architecture",
        default="x86_64-unknown-linux-gnu",
        choices=list(ARCHITECTURES.keys()),
        help="Target architecture (default: x86_64-unknown-linux-gnu)",
    )

    # Build configuration
    parser.add_argument(
        "--build-config",
        default="pgo+lto",
        choices=list(BUILD_CONFIGS.keys()),
        help="Build configuration (default: pgo+lto)",
    )

    # Content type
    parser.add_argument(
        "--content-type",
        default="install_only_stripped",
        choices=list(CONTENT_TYPES.keys()),
        help="Content type (default: install_only_stripped)",
    )

    # Windows variant (only for Windows architectures)
    parser.add_argument(
        "--windows-variant",
        choices=list(WINDOWS_VARIANTS.keys()),
        help="Windows variant (shared/static) - only for Windows architectures",
    )

    # Destination
    parser.add_argument(
        "--dest",
        default=".",
        help="Download destination directory (default: current directory)",
    )

    # List options
    parser.add_argument(
        "--list-options",
        action="store_true",
        help="List all available options and exit",
    )

    args = parser.parse_args()

    # Handle list options
    if args.list_options:
        list_available_options()
        return

    # Validate arguments
    if not validate_architecture(args.architecture):
        print(f"Error: Unsupported architecture '{args.architecture}'", file=sys.stderr)
        print("Use --list-options to see available architectures", file=sys.stderr)
        sys.exit(1)

    if not validate_build_config(args.build_config):
        print(
            f"Error: Unsupported build configuration '{args.build_config}'",
            file=sys.stderr,
        )
        print(
            "Use --list-options to see available build configurations", file=sys.stderr
        )
        sys.exit(1)

    if not validate_content_type(args.content_type):
        print(f"Error: Unsupported content type '{args.content_type}'", file=sys.stderr)
        print("Use --list-options to see available content types", file=sys.stderr)
        sys.exit(1)

    # Validate Windows variant if specified
    if args.windows_variant:
        if not args.architecture.startswith(
            ("i686-pc-windows-msvc", "x86_64-pc-windows-msvc")
        ):
            print(
                "Error: Windows variant can only be used with Windows architectures",
                file=sys.stderr,
            )
            sys.exit(1)
        if not validate_windows_variant(args.windows_variant):
            print(
                f"Error: Unsupported Windows variant '{args.windows_variant}'",
                file=sys.stderr,
            )
            sys.exit(1)

    # Get release information
    print(f"Fetching release information for {args.version}...")
    release_json = get_release(args.version)
    tag = release_json["tag_name"]

    # Select asset
    print(f"Looking for Python {args.python_version} build for {args.architecture}...")
    url, filename = select_asset(
        release_json,
        args.python_version,
        args.architecture,
        args.build_config,
        args.content_type,
        args.windows_variant,
    )

    if not url:
        print(
            f"No asset found for Python {args.python_version}, architecture {args.architecture}, "
            f"build config {args.build_config}, content type {args.content_type} in release {tag}",
            file=sys.stderr,
        )
        print("Available assets in this release:", file=sys.stderr)

        # Create target string for comparison
        target_string = f"cpython-{args.python_version}-{args.architecture}"
        if args.windows_variant:
            target_string += f"-{args.windows_variant}"
        target_string += f"-{args.build_config}-{args.content_type}"

        # Calculate Levenshtein distance for each asset and sort
        assets_with_distance = []
        for asset in release_json["assets"]:
            distance = levenshtein_distance(asset["name"], target_string)
            assets_with_distance.append((distance, asset["name"]))

        # Sort by distance (ascending) and show first 10
        assets_with_distance.sort(key=lambda x: x[0])
        for distance, asset_name in assets_with_distance[:10]:
            print(f"  {asset_name}", file=sys.stderr)
        if len(assets_with_distance) > 10:
            print(f"  ... and {len(assets_with_distance) - 10} more", file=sys.stderr)
        sys.exit(1)

    print(f"Downloading {filename} from release {tag}...")
    path = download_asset(url, filename, args.dest)
    print(f"Saved to {path}")


if __name__ == "__main__":
    main()
