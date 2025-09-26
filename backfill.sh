#!/usr/bin/env bash
set -euo pipefail

upload_dir="uploads"
thumb_dir="$upload_dir/thumbnails"

mkdir -p "$thumb_dir"

target_file="${1:-}" # optional file to override
override_time="${2:-00:00:05}"  # default timestamp

generate_thumbnail() {
    local file="$1"
    local thumb="$2"
    local timestamp="$3"

    echo "Generating thumbnail for $(basename "$file") at $timestamp as $thumb"
    if ! ffmpeg -y -ss "$timestamp" -i "$file" -vframes 1 -vf scale=400:-1 "$thumb" 2>/dev/null; then
        echo "Failed at $timestamp, retrying from start..."
        ffmpeg -y -i "$file" -vframes 1 -vf scale=400:-1 "$thumb"
    fi
}

normalize_path() {
    local file="$1"
    if [[ "$file" == "$upload_dir/"* ]]; then
        echo "$file"
    else
        echo "$upload_dir/$file"
    fi
}

if [[ -n "$target_file" ]]; then
    file_path=$(normalize_path "$target_file")
    filename=$(basename "$file_path")
    thumb="$thumb_dir/${filename}.jpg"

    if [[ ! -f "$file_path" ]]; then
        echo "File $file_path not found"
        exit 1
    fi

    generate_thumbnail "$file_path" "$thumb" "$override_time"
else
    find "$upload_dir" -maxdepth 1 -type f \( -iname "*.mp4" -o -iname "*.webm" -o -iname "*.mov" -o -iname "*.avi" -o -iname "*.mkv" \) | while read -r file; do
        filename=$(basename "$file")
        thumb="$thumb_dir/${filename}.jpg"

        if [[ -f "$thumb" ]]; then
            echo "Skipping $filename as thumbnail already exists"
            continue
        fi

        generate_thumbnail "$file" "$thumb" "$override_time"
    done
fi
