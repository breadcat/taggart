#!/usr/bin/env bash
set -euo pipefail

upload_dir="uploads"
thumb_dir="$upload_dir/thumbnails"

mkdir -p "$thumb_dir"

# Loop through all video files in uploads (not including thumbnails)
find "$upload_dir" -maxdepth 1 -type f \( -iname "*.mp4" -o -iname "*.webm" -o -iname "*.mov" -o -iname "*.avi" -o -iname "*.mkv" \) | while read -r file; do
    filename=$(basename "$file")
    thumb="$thumb_dir/${filename}.jpg"

    if [[ -f "$thumb" ]]; then
        echo "Skipping $filename as thumbnail already exists"
        continue
    fi

    echo "Generating thumbnail for $filename as $thumb"
    if ! ffmpeg -y -ss 00:00:05 -i "$file" -vframes 1 -vf scale=400:-1 "$thumb" 2>/dev/null; then
        echo "Failed at 5s, retrying from start..."
        ffmpeg -y -i "$file" -vframes 1 -vf scale=400:-1 "$thumb"
    fi
done
