import { useState, useEffect } from 'react';

// Simple in-memory cache for thumbnails keyed by "path:idx:size".
const thumbnailCache = new Map<string, string>();

export function useFaceThumbnail(
  imagePath: string,
  faceIndex: number,
  size: number = 64
): { src: string | null; loading: boolean } {
  const [src, setSrc] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const key = `${imagePath}:${faceIndex}:${size}`;
    const cached = thumbnailCache.get(key);
    if (cached) {
      setSrc(cached);
      setLoading(false);
      return;
    }

    let cancelled = false;
    setLoading(true);

    window.go?.app?.App?.GetFaceThumbnail?.(imagePath, faceIndex, size)
      .then((data) => {
        if (!cancelled && data) {
          thumbnailCache.set(key, data);
          setSrc(data);
        }
      })
      .catch(() => {
        // Silently fail — show placeholder
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [imagePath, faceIndex, size]);

  return { src, loading };
}
