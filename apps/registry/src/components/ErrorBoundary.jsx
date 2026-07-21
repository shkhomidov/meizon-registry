// ErrorBoundary — without one, a throw in any single component unmounts the
// whole React tree and the console goes blank (a "black page"), losing both the
// error and whatever the user was doing. This catches the throw, shows what
// happened, and keeps the rest of the app alive.
import { Component } from 'react'
import { AlertTriangle, RotateCcw } from 'lucide-react'

export default class ErrorBoundary extends Component {
  constructor(props) {
    super(props)
    this.state = { error: null }
  }

  static getDerivedStateFromError(error) {
    return { error }
  }

  componentDidCatch(error, info) {
    // Keep the stack in the console for debugging; the UI stays readable.
    console.error('[ErrorBoundary]', this.props.label || 'render error', error, info)
  }

  render() {
    const { error } = this.state
    if (!error) return this.props.children

    // A caller can degrade gracefully instead of showing the panel — e.g. the
    // PDF viewer falling back to the plain-text pane.
    if (this.props.fallback) {
      return typeof this.props.fallback === 'function'
        ? this.props.fallback(error, () => this.setState({ error: null }))
        : this.props.fallback
    }

    return (
      <div className="m-4 p-4 border border-st-red/40 bg-st-red/10 rounded-md max-w-2xl">
        <div className="flex items-center gap-2 text-[13px] font-medium text-text mb-1.5">
          <AlertTriangle size={15} className="text-st-red" />
          {this.props.label || 'Something went wrong in this view'}
        </div>
        <p className="text-[12.5px] text-text2 mb-2">
          The rest of the console is still working. The details are in the browser console.
        </p>
        <pre className="whitespace-pre-wrap text-[11.5px] text-muted bg-inset border border-border rounded p-2 mb-3 max-h-32 overflow-auto">
          {String(error?.message || error)}
        </pre>
        <button onClick={() => this.setState({ error: null })}
          className="inline-flex items-center gap-1.5 text-[12px] text-sage hover:underline">
          <RotateCcw size={13} /> Try again
        </button>
      </div>
    )
  }
}
