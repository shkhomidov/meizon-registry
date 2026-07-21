// Meizon UI kit — token-driven primitives. No hard-coded hex; every color is a
// Tailwind token name mapped to a CSS variable, so light/dark and rebrand are a
// one-file change.
import { Loader2, X } from 'lucide-react'

function cx(...parts) {
  return parts.filter(Boolean).join(' ')
}

// --- Page header + eyebrow ---
export function Page({ title, subtitle, action, children }) {
  return (
    <div className="animate-fade">
      <div className="flex items-start justify-between gap-4 mb-6">
        <div>
          <h1 className="text-[23px] font-medium tracking-tight leading-tight">{title}</h1>
          {subtitle && <p className="text-[13.5px] text-muted max-w-2xl mt-1">{subtitle}</p>}
        </div>
        {action}
      </div>
      {children}
    </div>
  )
}

export function Eyebrow({ children, className }) {
  return <div className={cx('eyebrow', className)}>{children}</div>
}

// --- Card ---
export function Card({ icon: Icon, title, action, children, className, bodyClass }) {
  return (
    <div className={cx('bg-card border border-border rounded-card', className)}>
      {(title || action) && (
        <div className="flex items-center justify-between gap-2 px-4 h-12 border-b border-border">
          <div className="flex items-center gap-2">
            {Icon && <Icon size={16} className="text-sage" />}
            <span className="text-sm font-medium">{title}</span>
          </div>
          {action}
        </div>
      )}
      <div className={cx('p-4', bodyClass)}>{children}</div>
    </div>
  )
}

// --- Stat tile ---
export function Stat({ value, label, caption, tone }) {
  const toneClass = tone ? `text-st-${tone}` : ''
  return (
    <div className="bg-card border border-border rounded-card p-4">
      <div className={cx('text-[26px] font-medium tabular-nums leading-none', toneClass)}>{value}</div>
      <div className="eyebrow mt-2">{label}</div>
      {caption && <div className="text-[12px] text-muted mt-1">{caption}</div>}
    </div>
  )
}

// --- Button ---
export function Button({ variant = 'primary', size = 'md', busy, icon: Icon, children, className, ...props }) {
  const base = 'inline-flex items-center justify-center gap-1.5 rounded-btn font-medium transition-colors disabled:opacity-50 disabled:cursor-not-allowed focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-sage'
  const variants = {
    primary: 'bg-sage text-sage-fg hover:bg-sage-hover',
    ghost: 'text-text border border-border hover:bg-surface',
    danger: 'bg-st-red text-white hover:opacity-90',
  }
  const sizes = { sm: 'text-[12px] px-2.5 py-1.5', md: 'text-[13px] px-3.5 py-2' }
  return (
    <button className={cx(base, variants[variant], sizes[size], className)} disabled={busy || props.disabled} {...props}>
      {busy ? <Loader2 size={15} className="animate-spin" /> : Icon && <Icon size={15} />}
      {children}
    </button>
  )
}

// --- Badge (status chip) ---
export function Badge({ tone = 'grey', children }) {
  const style = {
    backgroundColor: `color-mix(in srgb, var(--b-${toneVar(tone)}) 12%, transparent)`,
    borderColor: `color-mix(in srgb, var(--b-${toneVar(tone)}) 30%, transparent)`,
    color: `var(--b-${toneVar(tone)})`,
  }
  return (
    <span
      style={style}
      className="inline-flex items-center gap-1.5 font-mono text-[10px] font-medium uppercase tracking-[0.03em] px-1.5 py-0.5 rounded-badge border"
    >
      {children}
    </span>
  )
}

function toneVar(tone) {
  // status tokens are stored as --b-green etc.
  const map = { green: 'green', blue: 'blue', amber: 'amber', red: 'red', yellow: 'yellow', grey: 'grey' }
  return map[tone] || 'grey'
}

const STATUS_TONE = { DRAFT: 'grey', IN_REVIEW: 'amber', APPROVED: 'blue', PUBLISHED: 'green', DEPRECATED: 'red' }
export function StatusBadge({ status }) {
  return <Badge tone={STATUS_TONE[status] || 'grey'}>{status}</Badge>
}

// --- Table ---
export function Table({ children }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full border-collapse">{children}</table>
    </div>
  )
}
export function THead({ children }) {
  return <thead>{children}</thead>
}
export function TH({ children, className }) {
  return <th className={cx('text-left text-[11px] font-medium uppercase tracking-[0.05em] text-muted px-3.5 py-3 border-b border-border', className)}>{children}</th>
}
export function TR({ children, onClick }) {
  return <tr onClick={onClick} className={cx('border-b border-border last:border-0', onClick && 'hover:bg-inset cursor-pointer')}>{children}</tr>
}
export function TD({ children, className }) {
  return <td className={cx('px-3.5 py-3 text-[13px] text-text2 align-middle', className)}>{children}</td>
}

// --- Tabs ---
export function Tabs({ tabs, active, onChange }) {
  return (
    <div className="border-b border-border flex gap-1 mb-4">
      {tabs.map((t) => (
        <button
          key={t.key}
          onClick={() => onChange(t.key)}
          className={cx(
            'px-4 py-2.5 text-[13px] font-medium -mb-px border-b-2 transition-colors',
            active === t.key ? 'text-text border-sage' : 'text-muted border-transparent hover:text-text',
          )}
        >
          {t.label}
        </button>
      ))}
    </div>
  )
}

// --- Field / input ---
export function Field({ label, hint, children }) {
  return (
    <label className="block">
      {label && <span className="block font-mono text-[11px] uppercase tracking-[0.06em] text-muted mb-1.5">{label}</span>}
      {children}
      {hint && <span className="block text-[11px] text-muted mt-1">{hint}</span>}
    </label>
  )
}
const inputClass = 'w-full bg-surface border border-border rounded-md px-3 py-2 text-[13px] text-text outline-none focus:border-sage placeholder:text-subtle'
export function Input(props) {
  return <input {...props} className={cx(inputClass, props.className)} />
}
export function Textarea(props) {
  return <textarea {...props} className={cx(inputClass, 'resize-y', props.className)} />
}
export function Select({ children, ...props }) {
  return <select {...props} className={cx(inputClass, props.className)}>{children}</select>
}

// --- Dialog ---
export function Dialog({ title, onClose, children, footer }) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/50" onClick={onClose}>
      <div className="w-full max-w-lg bg-card border border-border rounded-card animate-fade" onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center justify-between px-5 h-12 border-b border-border">
          <span className="text-sm font-medium">{title}</span>
          <button onClick={onClose} className="text-muted hover:text-text"><X size={17} /></button>
        </div>
        <div className="p-6 flex flex-col gap-4">{children}</div>
        {footer && <div className="flex justify-end gap-2 px-6 py-4 border-t border-border">{footer}</div>}
      </div>
    </div>
  )
}

// --- Empty state ---
export function EmptyState({ icon: Icon, title, hint, action }) {
  return (
    <div className="flex flex-col items-center text-center py-14">
      {Icon && (
        <div className="w-12 h-12 rounded-xl flex items-center justify-center mb-3" style={{ background: 'color-mix(in srgb, var(--b-sage) 15%, transparent)' }}>
          <Icon size={22} className="text-sage" />
        </div>
      )}
      <div className="text-sm font-medium">{title}</div>
      {hint && <div className="text-[12.5px] text-muted mt-1 max-w-sm">{hint}</div>}
      {action && <div className="mt-4">{action}</div>}
    </div>
  )
}

// --- Avatar ---
export function Avatar({ name, size = 32 }) {
  const initials = (name || '?').split(/[@.\s]/).filter(Boolean).slice(0, 2).map((s) => s[0].toUpperCase()).join('')
  return (
    <div
      className="rounded-full flex items-center justify-center font-mono font-medium text-[11px] shrink-0"
      style={{
        width: size, height: size,
        background: 'color-mix(in srgb, var(--b-sage) 18%, transparent)',
        color: 'var(--b-sage)',
        boxShadow: '0 0 0 1px color-mix(in srgb, var(--b-sage) 40%, transparent)',
      }}
    >
      {initials}
    </div>
  )
}
