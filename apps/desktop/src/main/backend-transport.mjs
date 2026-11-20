// The desktop reaches its backend through the link layer.
export function operationTransport(descriptor) {
  return descriptor?.transport === "link";
}
