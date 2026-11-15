import { afterEach, expect, it, vi } from "vitest";
import { createQrReader } from "./qr-reader";
import { pairingPayload, qrFrame } from "./testdata/qr-frame";

const originalDetector = Object.getOwnPropertyDescriptor(window, "BarcodeDetector");

afterEach(() => {
  if (originalDetector) Object.defineProperty(window, "BarcodeDetector", originalDetector);
  else Reflect.deleteProperty(window, "BarcodeDetector");
  vi.restoreAllMocks();
});

function cameraFrame(inverted = false) {
  const frame = qrFrame(inverted);
  const video = document.createElement("video");
  Object.defineProperties(video, {
    readyState: { value: HTMLMediaElement.HAVE_CURRENT_DATA },
    videoWidth: { configurable: true, value: frame.width },
    videoHeight: { configurable: true, value: frame.height },
  });
  const drawImage = vi.fn();
  const getImageData = vi.fn().mockReturnValue(frame);
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue({ drawImage, getImageData } as unknown as CanvasRenderingContext2D);
  return { video, drawImage, getImageData };
}

it.each([false, true])("decodes a complete firmware-sized QR locally without BarcodeDetector (inverted=%s)", async (inverted) => {
  Reflect.deleteProperty(window, "BarcodeDetector");
  const { video } = cameraFrame(inverted);
  expect(pairingPayload).toHaveLength(199);
  expect(await createQrReader()(video)).toBe(pairingPayload);
});

it.each(["constructor", "detect"])("falls back when the native QR %s fails", async (failure) => {
  Object.defineProperty(window, "BarcodeDetector", {
    configurable: true,
    value: class {
      constructor() { if (failure === "constructor") throw new Error("unsupported"); }
      async detect() { throw new Error("unsupported"); }
    },
  });
  const { video } = cameraFrame();
  expect(await createQrReader()(video)).toBe(pairingPayload);
});

it("waits for video pixels instead of treating startup as a scan failure", async () => {
  Reflect.deleteProperty(window, "BarcodeDetector");
  const canvas = vi.spyOn(HTMLCanvasElement.prototype, "getContext");
  expect(await createQrReader()(document.createElement("video"))).toBeNull();
  expect(canvas).not.toHaveBeenCalled();
});

it("bounds fallback frame size and returns no code for a blank image", async () => {
  Reflect.deleteProperty(window, "BarcodeDetector");
  const { video, drawImage, getImageData } = cameraFrame();
  Object.defineProperties(video, {
    videoWidth: { value: 1920 }, videoHeight: { value: 1080 },
  });
  getImageData.mockImplementation((_x, _y, width, height) => ({ data: new Uint8ClampedArray(width * height * 4).fill(255) }));
  expect(await createQrReader()(video)).toBeNull();
  expect(drawImage).toHaveBeenCalledWith(video, 0, 0, 960, 540);
});
