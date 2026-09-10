/**
 * The glyphs the window is drawn with.
 *
 * Written here rather than pulled from an icon package, and the reason is
 * arithmetic: the design uses about a dozen, and a library is a dependency to
 * audit, update and ship for twelve shapes that are two lines each.
 *
 * They are strokes rather than filled shapes, at a 24 unit grid with a 2 unit
 * stroke, which is what keeps them legible at the fourteen pixels this window
 * draws them at and consistent with each other whatever the size.
 *
 * Every one is decoration: `aria-hidden`, no title, no label. What an icon
 * stands for is said in text beside it — sometimes visible, sometimes only to a
 * reader — because an icon that carries meaning alone is meaning nobody using a
 * screen reader receives.
 */

const glyphs = {
  /** A server, and a database on one. */
  database: (
    <>
      <ellipse cx="12" cy="6" rx="7" ry="3" />
      <path d="M5 6v12c0 1.7 3.1 3 7 3s7-1.3 7-3V6" />
      <path d="M5 12c0 1.7 3.1 3 7 3s7-1.3 7-3" />
    </>
  ),
  /** A schema: the thing that holds objects. */
  folder: (
    <path d="M3 7a2 2 0 0 1 2-2h3.4l2 2.5H19a2 2 0 0 1 2 2V18a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2Z" />
  ),
  table: (
    <>
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <path d="M3 9.5h18M9 9.5V20" />
    </>
  ),
  view: (
    <>
      <path d="M2 12s3.6-6 10-6 10 6 10 6-3.6 6-10 6-10-6-10-6Z" />
      <circle cx="12" cy="12" r="2.5" />
    </>
  ),
  sequence: <path d="M10 3 8 21M16.5 3l-2 18M3.5 8.5h17M2.5 15.5h17" />,
  /** Turned by CSS when the node it belongs to is open. */
  caret: <path d="m9.5 5.5 7 6.5-7 6.5" />,
  plus: <path d="M12 5v14M5 12h14" />,
  close: <path d="M6.5 6.5 17.5 17.5M17.5 6.5 6.5 17.5" />,
  search: (
    <>
      <circle cx="10.5" cy="10.5" r="6.5" />
      <path d="m15.5 15.5 5 5" />
    </>
  ),
  refresh: (
    <>
      <path d="M20 12a8 8 0 1 1-2.4-5.7" />
      <path d="M20.5 3.5V9H15" />
    </>
  ),
  /** What the foot of the navigator says: this is the server you are on. */
  plug: <path d="M9 2.5v6M15 2.5v6M6 8.5h12v3.5a6 6 0 0 1-12 0ZM12 18v3.5" />,
  /** The column a table is keyed on. */
  key: (
    <>
      <circle cx="8" cy="16" r="3.5" />
      <path d="m10.6 13.4 8.4-8.4M16 8l2.2 2.2M19 5l2.2 2.2" />
    </>
  ),
} as const;

/**
 * The few that are filled rather than drawn.
 *
 * A stroke reads as a label and a solid shape reads as an identity, which is
 * why the one on the product's own mark is solid and the ones naming rows are
 * not. Kept in a map of their own because the two need opposite SVG attributes,
 * and a flag on the component would be a caller deciding how a glyph is drawn.
 */
const solids = {
  databaseFill: (
    <>
      <path d="M12 2.2c3.9 0 7 1.3 7 2.9s-3.1 2.9-7 2.9-7-1.3-7-2.9 3.1-2.9 7-2.9Z" />
      <path d="M19 8.4v3.4c0 1.6-3.1 2.9-7 2.9s-7-1.3-7-2.9V8.4c1.5 1.1 4.1 1.7 7 1.7s5.5-.6 7-1.7Z" />
      <path d="M19 14.6V18c0 1.6-3.1 2.9-7 2.9S5 19.6 5 18v-3.4c1.5 1.1 4.1 1.7 7 1.7s5.5-.6 7-1.7Z" />
    </>
  ),
} as const;

export type IconName = keyof typeof glyphs | keyof typeof solids;

function isSolid(name: IconName): name is keyof typeof solids {
  return name in solids;
}

export function Icon({
  name,
  className,
}: {
  name: IconName;
  className?: string;
}): React.JSX.Element {
  const solid = isSolid(name);

  return (
    <svg
      className={className === undefined ? "icon" : `icon ${className}`}
      viewBox="0 0 24 24"
      fill={solid ? "currentColor" : "none"}
      stroke={solid ? "none" : "currentColor"}
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      {solid ? solids[name] : glyphs[name]}
    </svg>
  );
}
