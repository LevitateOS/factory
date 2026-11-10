// Offline controls for the existing real WebKit/native integration check.
// No product state, account, provider or inference fixture is installed.
(() => {
  const form = document.createElement('form');
  form.setAttribute('aria-label', 'Developer integration');
  form.innerHTML = `<label>Developer draft <input value="human text"></label>
    <label>Developer option <select><option value="one">One</option><option value="two">Two</option></select></label>
    <label>Developer consent <input type="checkbox"></label>
    <button type="button">Developer count</button>
    <button type="button" disabled>Developer disabled</button>
    <button type="button">Developer duplicate</button><button type="button">Developer duplicate</button>
    <output role="status" aria-label="Developer count result">0</output>
    <button type="button">Developer delayed</button>
    <button type="button">Developer lost reply</button>`;
  let count = 0;
  form.addEventListener('submit', event => event.preventDefault());
  form.querySelector('button').addEventListener('click', () => {
    form.querySelector('output').textContent = String(++count);
  });
  [...form.querySelectorAll('button')].find(button => button.textContent === 'Developer delayed').addEventListener('click', () => {
    setTimeout(() => { const button = document.createElement('button'); button.type = 'button'; button.textContent = 'Developer appeared'; form.append(button); }, 600);
  });
  form.querySelector('button:last-child').addEventListener('click', () => {
    form.querySelector('output').textContent = String(++count);
    const end = performance.now() + 250;
    while (performance.now() < end) { /* Deliberately withhold the dispatch reply. */ }
  });
  document.body.prepend(form);
})()
