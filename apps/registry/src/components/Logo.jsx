// Logo — the Barlos mark.
//
// Inline rather than an <img> so it inherits currentColor: the dark theme's
// --b-sage IS the brand green (#3DD68C), while the light theme uses a deeper
// #1A7F4B. Hardcoding the bright green would leave the mark washed out on a
// white background.
export default function Logo({ size = 20, className = 'text-sage' }) {
  return (
    <svg
      width={size} height={size} viewBox="0 0 100 100"
      fill="none" xmlns="http://www.w3.org/2000/svg"
      className={className} role="img" aria-label="Barlos"
    >
      <circle cx="50" cy="31" r="25" stroke="currentColor" strokeWidth="2.5" opacity="0.28" />
      <circle cx="50" cy="31" r="15.5" stroke="currentColor" strokeWidth="5.5" />
      <circle cx="28" cy="69" r="15.5" stroke="currentColor" strokeWidth="5.5" />
      <circle cx="72" cy="69" r="15.5" stroke="currentColor" strokeWidth="5.5" />
      <rect x="44.7" y="51.7" width="10.6" height="10.6" rx="1.4"
        transform="rotate(45 50 57)" fill="currentColor" />
    </svg>
  )
}
