type QRDetection = { rawValue: string };
type QRDetector = { detect: (source: HTMLVideoElement) => Promise<QRDetection[]> };
type QRDetectorConstructor = new (options: { formats: string[] }) => QRDetector;

/** Reads camera frames locally. The fallback is bundled, never loaded from a CDN. */
export function createQrReader(): (video: HTMLVideoElement) => Promise<string | null> {
  const NativeDetector = (window as Window & { BarcodeDetector?: QRDetectorConstructor }).BarcodeDetector;
  let native: QRDetector | undefined;
  try {
    if (NativeDetector) native = new NativeDetector({ formats: ["qr_code"] });
  } catch {
    // Some browsers expose the API but cannot construct a QR detector.
  }
  let decoder: Promise<typeof import("jsqr")> | undefined;
  let canvas: HTMLCanvasElement | undefined;

  return async (video) => {
    if (native) {
      try {
        return (await native.detect(video)).find((result) => result.rawValue)?.rawValue ?? null;
      } catch {
        native = undefined;
      }
    }
    if (video.readyState < HTMLMediaElement.HAVE_CURRENT_DATA || !video.videoWidth || !video.videoHeight) return null;
    decoder ??= import("jsqr");
    const { default: decode } = await decoder;
    canvas ??= document.createElement("canvas");
    const scale = Math.min(1, 960 / Math.max(video.videoWidth, video.videoHeight));
    const width = Math.max(1, Math.round(video.videoWidth * scale));
    const height = Math.max(1, Math.round(video.videoHeight * scale));
    if (canvas.width !== width) canvas.width = width;
    if (canvas.height !== height) canvas.height = height;
    const context = canvas.getContext("2d", { willReadFrequently: true });
    if (!context) throw new Error("Camera frame reader unavailable");
    context.drawImage(video, 0, 0, width, height);
    const frame = context.getImageData(0, 0, width, height);
    return decode(frame.data, width, height, { inversionAttempts: "attemptBoth" })?.data ?? null;
  };
}
