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
 * **Closing it does both halves, every time.** The element is closed and the
 * state is set, from the same handler, and both are safe to do twice: closing a
 * dialog that is closed does nothing and so does setting a false that is
 * already false. The arrangements that do one and let the other follow are
 * tidier and each has a way of leaving the two disagreeing — and when they
 * disagree the symptom is a button that visibly does nothing while Escape still
 * works, because Escape is the path the browser takes on its own.
 *
 * The `close` event is listened for on the element rather than through the
 * React prop, so that a key press is heard whether or not the framework maps
 * that event.
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

  // Escape closes a modal dialog without asking anybody, so the state that says
  // it is open has to hear about it or the two disagree — and the next attempt
  // to open it would find the element already closed and the state already
  // true, and do nothing at all.
  useEffect((): (() => void) => {
    const element = frame.current;
    element?.addEventListener("close", onClose);

    return (): void => {
      element?.removeEventListener("close", onClose);
    };
  }, [onClose]);

  function dismiss(): void {
    frame.current?.close();
    onClose();
  }

  return (
    <dialog
      ref={frame}
      className="dialog"
      aria-labelledby="dialog-title"
      // A click on the backdrop lands on the dialog element itself; a click on
      // anything inside it lands on that. It is the one way to tell them apart
      // without measuring where the pointer was.
      onClick={(event): void => {
        if (event.target === frame.current) {
          dismiss();
        }
      }}
    >
      <div className="dialog__header">
        <h2 id="dialog-title">{title}</h2>

        <button type="button" className="dialog__close" aria-label="Close" onClick={dismiss}>
          <Icon name="close" />
        </button>
      </div>

      <div className="dialog__body">{children}</div>
    </dialog>
  );
}
