import { useEffect, useRef } from "react";

/**
 * The frame the connection form is shown in.
 *
 * A native `<dialog>` opened with showModal, which is three things nobody has
 * to write: focus stays inside it, Escape closes it, and everything behind it
 * is inert. A panel built by hand would need a focus trap built by hand, and a
 * focus trap built by hand is where the accessibility of a dialog usually dies.
 *
 * It is opened imperatively because that is the only way the modal behaviour is
 * available — an `open` attribute gives a dialog that is merely visible, with
 * the page behind it still focusable.
 */
export function ConnectionDialog({
  open,
  onClose,
  children,
}: {
  open: boolean;
  onClose: () => void;
  children: React.ReactNode;
}): React.JSX.Element {
  const frame = useRef<HTMLDialogElement>(null);

  useEffect((): void => {
    const element = frame.current;
    if (!element) {
      return;
    }

    if (open && !element.open) {
      element.showModal();
    } else if (!open && element.open) {
      element.close();
    }
  }, [open]);

  return (
    <dialog
      ref={frame}
      className="dialog"
      aria-label="Connection"
      // Escape closes a modal dialog without asking anybody, so the state that
      // says it is open has to hear about it or the two disagree — and the next
      // attempt to open it would find the element already closed and the state
      // already true, and do nothing.
      onClose={onClose}
      // A click on the backdrop lands on the dialog element itself; a click on
      // anything inside it lands on that. It is the one way to tell them apart
      // without measuring where the pointer was.
      onClick={(event): void => {
        if (event.target === frame.current) {
          onClose();
        }
      }}
    >
      {children}
    </dialog>
  );
}
