import os
import sys


def main():
    binary_path = os.path.join(os.path.dirname(__file__), "caddysnake-cli")

    if not os.path.exists(binary_path):
        print(f"caddysnake-cli binary file not found at {binary_path}")
        sys.exit(1)

    os.execv(binary_path, [binary_path] + sys.argv[1:])
