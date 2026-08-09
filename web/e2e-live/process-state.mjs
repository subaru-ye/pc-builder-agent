export function isChildRunning(child) {
  return Boolean(child && child.exitCode == null && child.signalCode == null);
}
