// Edge space prevents the browser's scroll clamping from moving the gesture
// anchor when content becomes smaller than the viewport or crosses an edge.
export function scrollPadding(left: number, top: number, width: number, height: number, viewWidth: number, viewHeight: number) {
  return { left: Math.ceil(Math.max(0, -left)), top: Math.ceil(Math.max(0, -top)),
    right: Math.ceil(Math.max(0, left + viewWidth - width)), bottom: Math.ceil(Math.max(0, top + viewHeight - height)) };
}
