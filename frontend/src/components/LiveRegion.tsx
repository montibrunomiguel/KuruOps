// LiveRegion is a visually-hidden aria-live region for announcing dynamic
// UI changes to screen readers (e.g. TagPicker's "Tag added: X" / "Tag
// removed: X") -- the underlying content the user is looking at already
// updates visually, but nothing tells an assistive-technology user a change
// just happened unless it's announced through a live region like this one.
// aria-atomic="true" so the whole message is read as one unit rather than
// only the diffed portion.
export function LiveRegion({ message }: { message: string }) {
  return (
    <div className="visually-hidden" aria-live="polite" aria-atomic="true">
      {message}
    </div>
  );
}
