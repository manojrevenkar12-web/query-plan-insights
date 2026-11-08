import { useEffect, useRef, useState } from "react";
import { AnimatePresence, motion } from "framer-motion";
import { motion as m } from "../../theme/tokens";

const resetMs = 1600;

export function CodeBlock({ code, label }: { code: string; label: string }) {
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(code);
      setCopied(true);
      clearTimeout(timer.current);
      timer.current = setTimeout(() => setCopied(false), resetMs);
    } catch {
      setCopied(false);
    }
  };
  return (
    <pre className="codeblock" aria-label={label}>
      <code>{code}</code>
      <button className="copy" onClick={copy} aria-live="polite">
        <AnimatePresence mode="wait" initial={false}>
          <motion.span
            key={copied ? "done" : "copy"}
            initial={{ opacity: 0, y: 4 }}
            animate={{ opacity: 1, y: 0 }}
            exit={{ opacity: 0, y: -4 }}
            transition={{ duration: m.quick }}
            style={{ display: "inline-block" }}
          >
            {copied ? "Copied" : "Copy SQL"}
          </motion.span>
        </AnimatePresence>
      </button>
    </pre>
  );
}
