import gradio as gr

with gr.Blocks(title="jd-gradio") as demo:
    gr.Markdown("https://gradio.build-value.test")

demo.launch(server_port=7860)
