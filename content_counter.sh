#!/bin/bash

# Check if a directory name was provided as a command-line argument
if [ $# -ne 1 ]; then
    echo "Usage: $0 <dashboard|lens|visualization|map|index_pattern|security_rule|csp_rule_template|ml_module|tag>"
    exit 1
fi

# Get the current directory as the root directory
root_directory="$(pwd)/integrations"

# Extract the canvas directory name from the command-line argument
content_type="$1"

# Use the find command to locate all "canvas" directories with the specified name
find "$root_directory" -type d -name "$content_type" | while read -r content_directory; do
    # Use dirname to get the parent directory's name
    parent_directory=$(basename "$(dirname "$(dirname "$content_directory")")")
    
    # Use the find command to count the files in each "canvas" directory
    file_count=$(find "$content_directory" -maxdepth 1 -type f | wc -l)
    
    # Print the package name (parent directory name) and file count
    echo "Package: $parent_directory"
    echo "File Count: $file_count"
    echo ""
done