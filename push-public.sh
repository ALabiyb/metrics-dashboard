#!/usr/bin/env bash
# Push a NexBridge-branded version of this repo to GitHub (github remote).
# Run after pushing your changes to GitLab (origin/main).
# GitLab branch stays SoftNet; GitHub gets NexBridge Technologies branding.
set -euo pipefail

TEMP="public-push-$(date +%s)"
git checkout -b "$TEMP"

# Swap branding in public-facing files only
perl -pi -e '
  s/SoftNet Technologies/NexBridge Technologies/g;
  s/SoftNet AD/NexBridge AD/g;
  s/softnethq\.co\.tz/nexbridge.co.tz/g;
  s/realm `k8s dashboard`/realm `NexBridge AD`/g;
  s/192\.168\.200\.\d+/<internal-ip>/g;
  s/192\.168\.15\.\d+/<internal-ip>/g;
  s/SoftNet TV wall/NexBridge TV wall/g;
' README.md

git add README.md
git commit -m "public: NexBridge Technologies branding for GitHub"
git push github "$TEMP":main --force
git checkout main
git branch -D "$TEMP"

echo "Done — github/main updated with NexBridge branding."
