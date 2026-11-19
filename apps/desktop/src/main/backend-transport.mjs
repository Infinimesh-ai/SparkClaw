// The desktop reaches its backend through an operation transport. The link
// layer is the only one a joined profile uses; "iscp" remains only for the
// isolated qualification launcher's legacy profiles.
export function operationTransport(descriptor) {
  return descriptor?.transport === "link" || descriptor?.transport === "iscp";
}
