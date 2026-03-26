# Desktop Icons

Place your application icons here:

- `icon.png` — Source icon (1024×1024 PNG, used to generate others)
- `icon.ico` — Windows icon (multi-resolution .ico)
- `icon.icns` — macOS icon (Apple .icns format)
- `tray.png` — System tray icon (64×64 or 128×128, transparent background)
- `tray@2x.png` — Retina tray icon (128×128 or 256×256)

## Generating icons

```bash
# From a 1024x1024 source PNG:

# Generate .ico (requires ImageMagick)
convert icon.png -define icon:auto-resize=256,128,64,48,32,16 icon.ico

# Generate .icns (macOS only)
mkdir icon.iconset
sips -z 16 16     icon.png --out icon.iconset/icon_16x16.png
sips -z 32 32     icon.png --out icon.iconset/icon_16x16@2x.png
sips -z 32 32     icon.png --out icon.iconset/icon_32x32.png
sips -z 64 64     icon.png --out icon.iconset/icon_32x32@2x.png
sips -z 128 128   icon.png --out icon.iconset/icon_128x128.png
sips -z 256 256   icon.png --out icon.iconset/icon_128x128@2x.png
sips -z 256 256   icon.png --out icon.iconset/icon_256x256.png
sips -z 512 512   icon.png --out icon.iconset/icon_256x256@2x.png
sips -z 512 512   icon.png --out icon.iconset/icon_512x512.png
sips -z 1024 1024 icon.png --out icon.iconset/icon_512x512@2x.png
iconutil -c icns icon.iconset

# Generate tray icons
sips -z 64 64   icon.png --out tray.png
sips -z 128 128 icon.png --out tray@2x.png
```
