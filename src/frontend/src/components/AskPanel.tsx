import { useState, type FormEvent } from "react";
import { useMutation } from "@tanstack/react-query";
import { askQuery, type QueryResponse } from "../api";

export function AskPanel() {
  const [question, setQuestion] = useState("Is the payment service healthy?");
  const [namespace, setNamespace] = useState("prod");

  const mutation = useMutation<QueryResponse, Error, void>({
    mutationFn: () => askQuery(question, { namespace }),
  });

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (question.trim()) mutation.mutate();
  }

  const resp = mutation.data;

  return (
    <section className="panel">
      <h2>Ask</h2>
      <form className="ask-form" onSubmit={onSubmit}>
        <input
          className="ask-form__q"
          value={question}
          onChange={(e) => setQuestion(e.target.value)}
          placeholder="Ask about service / pod health, certs…"
          aria-label="Question"
        />
        <input
          className="ask-form__ns"
          value={namespace}
          onChange={(e) => setNamespace(e.target.value)}
          placeholder="namespace"
          aria-label="Namespace"
        />
        <button type="submit" disabled={mutation.isPending}>
          {mutation.isPending ? "Asking…" : "Ask"}
        </button>
      </form>

      {mutation.isError && <p className="error">Error: {mutation.error.message}</p>}

      {resp && (
        <div className="answer">
          <div className="answer__row">
            <span className={`badge badge--${resp.status}`}>{resp.status}</span>
            <span className="answer__confidence">
              confidence {(resp.confidence * 100).toFixed(0)}%
            </span>
          </div>
          <p className="answer__text">{resp.answer}</p>
          {resp.sources.length > 0 && (
            <p className="answer__sources">
              sources:{" "}
              {resp.sources.map((s) => (
                <code key={s}>{s}</code>
              ))}
            </p>
          )}
          {resp.trace_id && (
            <p className="answer__trace">
              trace_id: <code>{resp.trace_id}</code>
            </p>
          )}
        </div>
      )}
    </section>
  );
}
