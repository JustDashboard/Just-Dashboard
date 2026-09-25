use yew::prelude::*;

#[function_component]
fn App() -> Html {
    let count = use_state(|| 0);
    let onclick = {
        let count = count.clone();
        move |_| count.set(*count + 1)
    };
    html! {
        <main>
            <h1>{ "https://trunk.build-value.test" }</h1>
            <button {onclick}>{ format!("Clicked {} times", *count) }</button>
        </main>
    }
}

fn main() {
    yew::Renderer::<App>::new().render();
}
