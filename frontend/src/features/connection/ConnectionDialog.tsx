import { useEffect, useRef } from "react";

import { Icon } from "../../ui/Icon";

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
 *
 * **Every way of closing it closes the element, and the element tells React.**
 * The button and the backdrop call close() on the dialog exactly as Escape
 * does; the `close` event that follows is the one place the state is set. The
 * other arrangement — the button setting state and an effect closing the
 * element — has two paths to the same outcome and only one of them is the one
 * the browser already takes, so the two can disagree and the visible one is the
 * button that does nothing.
 */
export function ConnectionDialog({
  open,
  title,
  onClose,
  children,
}: {
  open: boolean;
  title: string;
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
      aria-labelledby="dialog-title"
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
          frame.current.close();
        }
      }}
    >
      <div className="dialog__header">
        <h2 id="dialog-title">{title}</h2>

        <button
          type="button"
          className="dialog__close"
          aria-label="Close"
          onClick={(): void => {
            frame.current?.close();
          }}
        >
          <Icon name="close" />
        </button>
      </div>

      <div className="dialog__body">{children}</div>
    </dialog>
  );
}
