// make_icon renders the icon SVG, the single source of every icon:
//   make_icon <icon.svg> <out.iconset>                      macOS .iconset (for iconutil): the 824/1024 rounded square
//   make_icon --png <icon.svg> <px> <out.png> square|rounded one PNG, full bleed (the OS masks it: iPhone, Android
//                                                           maskable) or the rounded app shape on a transparent canvas
import AppKit

let args = CommandLine.arguments
func usage() -> Never {
    FileHandle.standardError.write(Data("usage: make_icon <icon.svg> <out.iconset> | make_icon --png <icon.svg> <px> <out.png> square|rounded\n".utf8))
    exit(2)
}
let pngMode = args.count == 6 && args[1] == "--png"
guard args.count == 3 || pngMode, let svg = NSImage(contentsOfFile: pngMode ? args[2] : args[1]) else { usage() }

func square(_ px: Int) -> Data {
    let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: px, pixelsHigh: px, bitsPerSample: 8,
                               samplesPerPixel: 4, hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB,
                               bytesPerRow: 0, bitsPerPixel: 0)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    svg.draw(in: NSRect(x: 0, y: 0, width: px, height: px))
    NSGraphicsContext.restoreGraphicsState()
    return rep.representation(using: .png, properties: [:])!
}

func render(_ px: Int) -> Data {
    let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: px, pixelsHigh: px, bitsPerSample: 8,
                               samplesPerPixel: 4, hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB,
                               bytesPerRow: 0, bitsPerPixel: 0)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    let s = CGFloat(px) / 1024
    let body = NSRect(x: 100 * s, y: 100 * s, width: 824 * s, height: 824 * s)
    NSBezierPath(roundedRect: body, xRadius: 185 * s, yRadius: 185 * s).addClip()
    svg.draw(in: body)
    NSGraphicsContext.restoreGraphicsState()
    return rep.representation(using: .png, properties: [:])!
}

if pngMode {
    guard let px = Int(args[3]), px > 0, px <= 2048, ["square", "rounded"].contains(args[5]) else { usage() }
    try (args[5] == "square" ? square(px) : render(px)).write(to: URL(fileURLWithPath: args[4]))
    exit(0)
}
let out = URL(fileURLWithPath: args[2])
try FileManager.default.createDirectory(at: out, withIntermediateDirectories: true)
for pt in [16, 32, 128, 256, 512] {
    try render(pt).write(to: out.appendingPathComponent("icon_\(pt)x\(pt).png"))
    try render(pt * 2).write(to: out.appendingPathComponent("icon_\(pt)x\(pt)@2x.png"))
}
