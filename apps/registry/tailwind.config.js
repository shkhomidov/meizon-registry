/** Meizon design system — tokens mapped to semantic Tailwind names. Never
 *  hard-code hex in components; use these names so light/dark and rebrand are a
 *  one-file change (index.css). */
export default {
  content: ['./index.html', './src/**/*.{js,jsx}'],
  darkMode: ['class', '[data-theme="dark"]'],
  theme: {
    extend: {
      colors: {
        bg: 'var(--b-bg)',
        card: 'var(--b-card)',
        surface: 'var(--b-surface)',
        border: 'var(--b-border)',
        inset: 'var(--b-inset)',
        subtle: 'var(--b-subtle)',
        text: 'var(--b-text)',
        text2: 'var(--b-text2)',
        muted: 'var(--b-muted)',
        sage: 'var(--b-sage)',
        'sage-hover': 'var(--b-sage-h)',
        'sage-fg': 'var(--b-sage-fg)',
        'st-green': 'var(--b-green)',
        'st-blue': 'var(--b-blue)',
        'st-amber': 'var(--b-amber)',
        'st-red': 'var(--b-red)',
        'st-yellow': 'var(--b-yellow)',
        'st-grey': 'var(--b-grey)',
      },
      fontFamily: {
        sans: ['Inter', 'system-ui', 'sans-serif'],
        mono: ['"JetBrains Mono"', 'ui-monospace', 'monospace'],
      },
      borderRadius: {
        card: '12px',
        btn: '8px',
        badge: '4px',
      },
      keyframes: {
        fade: {
          '0%': { opacity: '0', transform: 'translateY(4px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' },
        },
        pulse2: {
          '0%,100%': { opacity: '1' },
          '50%': { opacity: '0.4' },
        },
      },
      animation: {
        fade: 'fade .2s ease-out',
        pulse2: 'pulse2 2s ease-in-out infinite',
      },
    },
  },
  plugins: [],
}
